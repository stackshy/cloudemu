// Package persist snapshots cloudemu provider state to disk and restores it, so
// emulated resources survive a stop/start of the standalone server.
//
// A mock that implements internal/snapshot.Snapshottable serializes its own
// full state (identity-preserving: resource ids and id-string cross-references
// survive), captured under ProviderState.Services keyed by service name. For a
// mock that does not implement it yet, Export/Restore fall back to the bespoke
// path that reads and writes through the same provider-agnostic driver
// interfaces the seed package uses — so one snapshot format still spans AWS,
// Azure, and GCP as more services migrate. Either way the on-disk file is
// human-readable, diffable JSON rather than an opaque binary blob.
package persist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/seed"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// SchemaVersion is the on-disk snapshot format version. Bumped to 2 for the
// per-driver identity-preserving layout (a migrated service's mock serializes
// its own state under ProviderState.Services); a v1 snapshot is rejected on
// load. Snapshots are a dev-only convenience, so a clean break is acceptable.
const SchemaVersion = 2

const (
	defaultContentType = "application/octet-stream"

	dirPerm = 0o755

	// Service names keying ProviderState.Services for the generic (per-driver
	// Snapshottable) path.
	svcStorage  = "storage"
	svcDatabase = "database"
	svcSecrets  = "secrets"
	svcCompute  = "compute"
)

// Sentinel errors so callers (and err113) get static, wrappable failures.
var (
	errNoDriver        = errors.New("snapshot names a resource kind with no driver in the target")
	errSchema          = errors.New("unsupported snapshot schema version")
	errNotSnapshotable = errors.New("snapshot carries per-driver state for a service whose target driver is not snapshottable")
)

// Snapshot is a multi-cloud, point-in-time capture of provider state, written
// as one JSON document.
type Snapshot struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Meta          *Meta                    `json:"meta,omitempty"`
	Providers     map[string]ProviderState `json:"providers,omitempty"`
}

// Meta is optional descriptive header for a named snapshot (the auto
// persist-on-stop file leaves it nil). It lets tooling describe a snapshot
// without restoring it.
type Meta struct {
	Name            string   `json:"name,omitempty"`
	CreatedAt       string   `json:"createdAt,omitempty"`
	CloudemuVersion string   `json:"cloudemuVersion,omitempty"`
	Providers       []string `json:"providers,omitempty"`
}

// ProviderState is a single provider's persisted resources. Services holds the
// per-driver identity-preserving snapshots (keyed by service name) for mocks
// that implement snapshot.Snapshottable; the remaining fields hold the bespoke
// fallback capture for mocks not yet migrated. For any given service exactly one
// representation is populated.
type ProviderState struct {
	Services  map[string]json.RawMessage `json:"services,omitempty"`
	Buckets   []Bucket                   `json:"buckets,omitempty"`
	Tables    []Table                    `json:"tables,omitempty"`
	Secrets   []Secret                   `json:"secrets,omitempty"`
	Instances []Instance                 `json:"instances,omitempty"`
}

// Bucket is an object-storage bucket and its objects.
type Bucket struct {
	Name    string   `json:"name"`
	Objects []Object `json:"objects,omitempty"`
}

// Object is a stored object. Body is nil in a metadata-only snapshot; JSON
// encodes it as base64.
type Object struct {
	Key         string `json:"key"`
	ContentType string `json:"contentType,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// Table is a NoSQL table, its secondary indexes, and its items.
type Table struct {
	Name         string               `json:"name"`
	PartitionKey string               `json:"partitionKey"`
	SortKey      string               `json:"sortKey,omitempty"`
	GSIs         []dbdriver.GSIConfig `json:"gsis,omitempty"`
	Items        []map[string]any     `json:"items,omitempty"`
}

// Secret is a secret and its current value. The value is always captured (a
// secret without its value can't be restored usefully); metadata-only affects
// only bulk object bodies.
type Secret struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Value       []byte            `json:"value,omitempty"`
}

// Instance is a compute instance's launch shape. Restore recreates it via
// RunInstances, so the emulator assigns a fresh instance ID/IP — image, type,
// and tags are preserved, identifiers are not.
type Instance struct {
	ImageID      string            `json:"imageId"`
	InstanceType string            `json:"instanceType"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// Options controls what Export captures.
type Options struct {
	// IncludeAssets includes object bodies. When false (the default) the
	// snapshot is metadata-only — bucket/object/table structure without the
	// object bytes — which keeps the file small.
	IncludeAssets bool
}

// ExportAll captures the state of every provider in targets into one Snapshot.
// It is the whole-emulator core shared by the persist-on-stop path and the
// snapshot admin endpoint.
func ExportAll(ctx context.Context, targets map[string]seed.Target, opts Options) (Snapshot, error) {
	snap := Snapshot{SchemaVersion: SchemaVersion, Providers: make(map[string]ProviderState, len(targets))}

	for name, t := range targets {
		ps, err := Export(ctx, t, opts)
		if err != nil {
			return Snapshot{}, fmt.Errorf("export %s: %w", name, err)
		}

		snap.Providers[name] = ps
	}

	return snap, nil
}

// RestoreAll restores each provider present in snap into the matching target.
// Providers in the snapshot with no matching running target are skipped, and
// targets should be freshly rebuilt (empty) before calling.
func RestoreAll(ctx context.Context, snap *Snapshot, targets map[string]seed.Target) error {
	for name := range snap.Providers {
		t, ok := targets[name]
		if !ok {
			continue
		}

		ps := snap.Providers[name]
		if err := Restore(ctx, t, &ps); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}

	return nil
}

// Export captures a provider's current state. For each service whose mock
// implements snapshot.Snapshottable, its identity-preserving self-snapshot is
// stored under ProviderState.Services; every other service falls back to the
// bespoke driver-replay capture. A nil driver for a kind contributes nothing.
func Export(ctx context.Context, t seed.Target, opts Options) (ProviderState, error) {
	ps := ProviderState{Services: map[string]json.RawMessage{}}

	if err := exportKind(ctx, &ps, svcStorage, t.Storage, opts.IncludeAssets, func() error {
		buckets, err := exportBuckets(ctx, t.Storage, opts.IncludeAssets)
		ps.Buckets = buckets
		return err
	}); err != nil {
		return ProviderState{}, err
	}

	if err := exportKind(ctx, &ps, svcDatabase, t.Database, opts.IncludeAssets, func() error {
		tables, err := exportTables(ctx, t.Database)
		ps.Tables = tables
		return err
	}); err != nil {
		return ProviderState{}, err
	}

	if err := exportKind(ctx, &ps, svcSecrets, t.Secrets, opts.IncludeAssets, func() error {
		secrets, err := exportSecrets(ctx, t.Secrets)
		ps.Secrets = secrets
		return err
	}); err != nil {
		return ProviderState{}, err
	}

	if err := exportKind(ctx, &ps, svcCompute, t.Compute, opts.IncludeAssets, func() error {
		instances, err := exportInstances(ctx, t.Compute)
		ps.Instances = instances
		return err
	}); err != nil {
		return ProviderState{}, err
	}

	if len(ps.Services) == 0 {
		ps.Services = nil
	}

	return ps, nil
}

// exportKind stores d's self-snapshot under name when d implements
// Snapshottable; otherwise it runs the bespoke fallback capture.
func exportKind[D any](ctx context.Context, ps *ProviderState, name string, d D, includeAssets bool, bespoke func() error) error {
	if s, ok := any(d).(snapshot.Snapshottable); ok {
		raw, err := s.Snapshot(ctx, includeAssets)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", name, err)
		}

		ps.Services[name] = raw

		return nil
	}

	return bespoke()
}

// Restore writes a provider state back into what should be a freshly-built
// (empty) provider. A service captured under Services is restored through its
// mock's Snapshottable.Restore; otherwise the bespoke driver-replay path runs.
func Restore(ctx context.Context, t seed.Target, ps *ProviderState) error {
	if err := restoreKind(ctx, ps, svcStorage, t.Storage, func() error {
		return restoreBuckets(ctx, t.Storage, ps.Buckets)
	}); err != nil {
		return err
	}

	if err := restoreKind(ctx, ps, svcDatabase, t.Database, func() error {
		return restoreTables(ctx, t.Database, ps.Tables)
	}); err != nil {
		return err
	}

	if err := restoreKind(ctx, ps, svcSecrets, t.Secrets, func() error {
		return restoreSecrets(ctx, t.Secrets, ps.Secrets)
	}); err != nil {
		return err
	}

	return restoreKind(ctx, ps, svcCompute, t.Compute, func() error {
		return restoreInstances(ctx, t.Compute, ps.Instances)
	})
}

// restoreKind restores name's per-driver snapshot through d's Snapshottable when
// the snapshot carries one; otherwise it runs the bespoke fallback restore.
func restoreKind[D any](ctx context.Context, ps *ProviderState, name string, d D, bespoke func() error) error {
	raw, ok := ps.Services[name]
	if !ok {
		return bespoke()
	}

	s, ok := any(d).(snapshot.Snapshottable)
	if !ok {
		return fmt.Errorf("%w: %s", errNotSnapshotable, name)
	}

	if err := s.Restore(ctx, raw); err != nil {
		return fmt.Errorf("restore %s: %w", name, err)
	}

	return nil
}

func exportBuckets(ctx context.Context, d storagedriver.Bucket, includeAssets bool) ([]Bucket, error) {
	if d == nil {
		return nil, nil
	}

	infos, err := d.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}

	out := make([]Bucket, 0, len(infos))

	for _, bi := range infos {
		objs, err := exportObjects(ctx, d, bi.Name, includeAssets)
		if err != nil {
			return nil, err
		}

		out = append(out, Bucket{Name: bi.Name, Objects: objs})
	}

	return out, nil
}

func exportObjects(ctx context.Context, d storagedriver.Bucket, bucket string, includeAssets bool) ([]Object, error) {
	var (
		out   []Object
		token string
	)

	for {
		res, err := d.ListObjects(ctx, bucket, storagedriver.ListOptions{PageToken: token})
		if err != nil {
			return nil, fmt.Errorf("list objects in %q: %w", bucket, err)
		}

		for _, oi := range res.Objects {
			obj := Object{Key: oi.Key, ContentType: oi.ContentType}

			if includeAssets {
				full, gErr := d.GetObject(ctx, bucket, oi.Key)
				if gErr != nil {
					return nil, fmt.Errorf("get object %s/%s: %w", bucket, oi.Key, gErr)
				}

				obj.Body = full.Data
			}

			out = append(out, obj)
		}

		if !res.IsTruncated || res.NextPageToken == "" {
			break
		}

		token = res.NextPageToken
	}

	return out, nil
}

func exportTables(ctx context.Context, d dbdriver.Database) ([]Table, error) {
	if d == nil {
		return nil, nil
	}

	names, err := d.ListTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}

	out := make([]Table, 0, len(names))

	for _, name := range names {
		cfg, err := d.DescribeTable(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("describe table %q: %w", name, err)
		}

		items, err := scanAll(ctx, d, name)
		if err != nil {
			return nil, err
		}

		out = append(out, Table{
			Name:         name,
			PartitionKey: cfg.PartitionKey,
			SortKey:      cfg.SortKey,
			GSIs:         cfg.GSIs,
			Items:        items,
		})
	}

	return out, nil
}

func scanAll(ctx context.Context, d dbdriver.Database, table string) ([]map[string]any, error) {
	var (
		items []map[string]any
		token string
	)

	for {
		res, err := d.Scan(ctx, dbdriver.ScanInput{Table: table, PageToken: token})
		if err != nil {
			return nil, fmt.Errorf("scan table %q: %w", table, err)
		}

		items = append(items, res.Items...)

		if res.NextPageToken == "" {
			break
		}

		token = res.NextPageToken
	}

	return items, nil
}

func exportSecrets(ctx context.Context, d secretsdriver.Secrets) ([]Secret, error) {
	if d == nil {
		return nil, nil
	}

	infos, err := d.ListSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	out := make([]Secret, 0, len(infos))

	for _, si := range infos {
		ver, err := d.GetSecretValue(ctx, si.Name, "")
		if err != nil {
			return nil, fmt.Errorf("get secret value %q: %w", si.Name, err)
		}

		s := Secret{Name: si.Name, Description: si.Description, Tags: si.Tags}
		if ver != nil {
			s.Value = ver.Value
		}

		out = append(out, s)
	}

	return out, nil
}

func exportInstances(ctx context.Context, d computedriver.Compute) ([]Instance, error) {
	if d == nil {
		return nil, nil
	}

	insts, err := d.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("describe instances: %w", err)
	}

	out := make([]Instance, 0, len(insts))

	for i := range insts {
		in := &insts[i]
		// Terminated instances are tombstones; recreating them would resurrect
		// deleted resources on restore.
		if in.State == "terminated" {
			continue
		}

		out = append(out, Instance{ImageID: in.ImageID, InstanceType: in.InstanceType, Tags: in.Tags})
	}

	return out, nil
}

func restoreBuckets(ctx context.Context, d storagedriver.Bucket, buckets []Bucket) error {
	if len(buckets) == 0 {
		return nil
	}

	if d == nil {
		return fmt.Errorf("%w: buckets", errNoDriver)
	}

	for _, b := range buckets {
		if err := d.CreateBucket(ctx, b.Name); err != nil {
			return fmt.Errorf("restore bucket %q: %w", b.Name, err)
		}

		for _, o := range b.Objects {
			ct := o.ContentType
			if ct == "" {
				ct = defaultContentType
			}

			if err := d.PutObject(ctx, b.Name, o.Key, o.Body, ct, nil); err != nil {
				return fmt.Errorf("restore object %s/%s: %w", b.Name, o.Key, err)
			}
		}
	}

	return nil
}

func restoreTables(ctx context.Context, d dbdriver.Database, tables []Table) error {
	if len(tables) == 0 {
		return nil
	}

	if d == nil {
		return fmt.Errorf("%w: tables", errNoDriver)
	}

	for _, tb := range tables {
		cfg := dbdriver.TableConfig{Name: tb.Name, PartitionKey: tb.PartitionKey, SortKey: tb.SortKey, GSIs: tb.GSIs}
		if err := d.CreateTable(ctx, cfg); err != nil {
			return fmt.Errorf("restore table %q: %w", tb.Name, err)
		}

		for i, item := range tb.Items {
			if err := d.PutItem(ctx, tb.Name, expr.RetypeItem(item)); err != nil {
				return fmt.Errorf("restore table %q item %d: %w", tb.Name, i, err)
			}
		}
	}

	return nil
}

func restoreSecrets(ctx context.Context, d secretsdriver.Secrets, secrets []Secret) error {
	if len(secrets) == 0 {
		return nil
	}

	if d == nil {
		return fmt.Errorf("%w: secrets", errNoDriver)
	}

	for _, s := range secrets {
		cfg := secretsdriver.SecretConfig{Name: s.Name, Description: s.Description, Tags: s.Tags}
		if _, err := d.CreateSecret(ctx, cfg, s.Value); err != nil {
			return fmt.Errorf("restore secret %q: %w", s.Name, err)
		}
	}

	return nil
}

func restoreInstances(ctx context.Context, d computedriver.Compute, instances []Instance) error {
	if len(instances) == 0 {
		return nil
	}

	if d == nil {
		return fmt.Errorf("%w: instances", errNoDriver)
	}

	for _, in := range instances {
		cfg := computedriver.InstanceConfig{ImageID: in.ImageID, InstanceType: in.InstanceType, Tags: in.Tags}
		if _, err := d.RunInstances(ctx, cfg, 1); err != nil {
			return fmt.Errorf("restore instance (%s): %w", in.ImageID, err)
		}
	}

	return nil
}

// WriteFile writes the snapshot as indented JSON, creating parent directories.
func (s Snapshot) WriteFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// Write to a temp file in the same dir, then rename onto the target. Rename
	// is atomic on the same filesystem, so an interrupted write (disk-full, OOM,
	// SIGKILL) leaves the previous snapshot — or none — but never a truncated
	// file that would fail the next start.
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)

		return err
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)

		return err
	}

	return nil
}

// ReadFile loads a snapshot from disk, rejecting an unknown schema version.
func ReadFile(path string) (Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}

	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot %q: %w", path, err)
	}

	if s.SchemaVersion != SchemaVersion {
		return Snapshot{}, fmt.Errorf("%w: got %d, want %d", errSchema, s.SchemaVersion, SchemaVersion)
	}

	return s, nil
}

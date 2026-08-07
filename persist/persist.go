// Package persist snapshots cloudemu provider state to disk and restores it, so
// emulated resources survive a stop/start of the standalone server.
//
// State can't be serialized generically (Go has no pickle; the in-memory value
// types hold mutexes, funcs, and nested stores that no reflection codec can
// round-trip). Instead Export/Restore read and write through the same
// provider-agnostic driver interfaces the seed package uses — so one snapshot
// format spans AWS, Azure, and GCP, and the on-disk file is human-readable and
// diffable JSON rather than an opaque binary blob.
package persist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stackshy/cloudemu/v2/seed"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// SchemaVersion is the on-disk snapshot format version. Bump it on a
// backward-incompatible change to the JSON shape.
const SchemaVersion = 1

const (
	defaultContentType = "application/octet-stream"

	dirPerm = 0o755
)

// Sentinel errors so callers (and err113) get static, wrappable failures.
var (
	errNoDriver = errors.New("snapshot names a resource kind with no driver in the target")
	errSchema   = errors.New("unsupported snapshot schema version")
)

// Snapshot is a multi-cloud, point-in-time capture of provider state, written
// as one JSON document.
type Snapshot struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Providers     map[string]ProviderState `json:"providers,omitempty"`
}

// ProviderState is a single provider's persisted resources.
type ProviderState struct {
	Buckets   []Bucket   `json:"buckets,omitempty"`
	Tables    []Table    `json:"tables,omitempty"`
	Secrets   []Secret   `json:"secrets,omitempty"`
	Instances []Instance `json:"instances,omitempty"`
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

// Export reads a provider's current state through its drivers. A nil driver for
// a kind simply contributes nothing (that kind isn't persisted).
func Export(ctx context.Context, t seed.Target, opts Options) (ProviderState, error) {
	buckets, err := exportBuckets(ctx, t.Storage, opts.IncludeAssets)
	if err != nil {
		return ProviderState{}, err
	}

	tables, err := exportTables(ctx, t.Database)
	if err != nil {
		return ProviderState{}, err
	}

	secrets, err := exportSecrets(ctx, t.Secrets)
	if err != nil {
		return ProviderState{}, err
	}

	instances, err := exportInstances(ctx, t.Compute)
	if err != nil {
		return ProviderState{}, err
	}

	return ProviderState{Buckets: buckets, Tables: tables, Secrets: secrets, Instances: instances}, nil
}

// Restore writes a provider state back through its drivers, into what should be
// a freshly-built (empty) provider.
func Restore(ctx context.Context, t seed.Target, ps *ProviderState) error {
	if err := restoreBuckets(ctx, t.Storage, ps.Buckets); err != nil {
		return err
	}

	if err := restoreTables(ctx, t.Database, ps.Tables); err != nil {
		return err
	}

	if err := restoreSecrets(ctx, t.Secrets, ps.Secrets); err != nil {
		return err
	}

	return restoreInstances(ctx, t.Compute, ps.Instances)
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
			if err := d.PutItem(ctx, tb.Name, item); err != nil {
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

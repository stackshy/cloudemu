// Package seed loads declarative fixtures into a cloudemu driver set, so teams
// can check JSON fixtures into their repo and bring the emulator up to a known
// state deterministically.
//
// Fixtures are provider-agnostic: they name resource kinds (buckets, tables,
// secrets, instances), and Apply writes them through the driver interfaces —
// which every provider implements — so the same fixture file seeds AWS, Azure,
// or GCP depending on which drivers you pass in the Target.
package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Fixtures is the declarative resource set. Every section is optional; an empty
// Fixtures applies nothing.
type Fixtures struct {
	Buckets   []Bucket   `json:"buckets,omitempty"`
	Tables    []Table    `json:"tables,omitempty"`
	Secrets   []Secret   `json:"secrets,omitempty"`
	Instances []Instance `json:"instances,omitempty"`
}

// Bucket is an object-storage bucket and its initial objects.
type Bucket struct {
	Name    string   `json:"name"`
	Objects []Object `json:"objects,omitempty"`
}

// Object is a single stored object. Body is the literal string content.
type Object struct {
	Key         string `json:"key"`
	Body        string `json:"body,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// Table is a NoSQL table and its initial items.
type Table struct {
	Name         string           `json:"name"`
	PartitionKey string           `json:"partitionKey"`
	SortKey      string           `json:"sortKey,omitempty"`
	Items        []map[string]any `json:"items,omitempty"`
}

// Secret is a secret and its value.
type Secret struct {
	Name        string `json:"name"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// Instance is one or more identical compute instances.
type Instance struct {
	ImageID      string `json:"imageId"`
	InstanceType string `json:"instanceType"`
	Count        int    `json:"count,omitempty"` // defaults to 1
	Name         string `json:"name,omitempty"`  // becomes the Name tag
}

// Target holds the drivers a fixture set is applied to. Only the drivers a
// given fixture touches need to be non-nil; a fixture that names a kind whose
// driver is nil is a clear error rather than a silent skip.
type Target struct {
	Storage  storagedriver.Bucket
	Database dbdriver.Database
	Secrets  secretsdriver.Secrets
	Compute  computedriver.Compute
}

// Load parses fixtures from JSON bytes.
func Load(data []byte) (Fixtures, error) {
	var f Fixtures
	if err := json.Unmarshal(data, &f); err != nil {
		return Fixtures{}, fmt.Errorf("parse fixtures: %w", err)
	}
	return f, nil
}

// LoadFS reads and parses a fixture file from fsys — pass an embed.FS to load
// go:embed-ed fixtures.
func LoadFS(fsys fs.FS, name string) (Fixtures, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Fixtures{}, fmt.Errorf("read fixtures %q: %w", name, err)
	}
	return Load(data)
}

// ResourceCount is the number of individual resources the fixtures describe
// (each object, item, and instance counts, not just top-level entries).
func (f Fixtures) ResourceCount() int {
	n := 0
	for _, b := range f.Buckets {
		n += 1 + len(b.Objects)
	}
	for _, tb := range f.Tables {
		n += 1 + len(tb.Items)
	}
	n += len(f.Secrets)
	for _, in := range f.Instances {
		c := in.Count
		if c < 1 {
			c = 1
		}
		n += c
	}
	return n
}

// Validate checks that every fixture has the fields it needs and that a driver
// exists for every kind it uses, so Apply can't silently create broken
// resources (e.g. a table with no partition key, whose items would all collapse
// to one key) or half-seed and then fail.
func (f Fixtures) Validate(t Target) error {
	if len(f.Buckets) > 0 && t.Storage == nil {
		return fmt.Errorf("fixtures declare buckets but Target.Storage is nil")
	}
	for _, b := range f.Buckets {
		if b.Name == "" {
			return fmt.Errorf("bucket: name is required")
		}
		for _, o := range b.Objects {
			if o.Key == "" {
				return fmt.Errorf("bucket %q: object key is required", b.Name)
			}
		}
	}
	if len(f.Tables) > 0 && t.Database == nil {
		return fmt.Errorf("fixtures declare tables but Target.Database is nil")
	}
	for _, tb := range f.Tables {
		if tb.Name == "" {
			return fmt.Errorf("table: name is required")
		}
		if tb.PartitionKey == "" {
			return fmt.Errorf("table %q: partitionKey is required", tb.Name)
		}
	}
	if len(f.Secrets) > 0 && t.Secrets == nil {
		return fmt.Errorf("fixtures declare secrets but Target.Secrets is nil")
	}
	for _, s := range f.Secrets {
		if s.Name == "" {
			return fmt.Errorf("secret: name is required")
		}
	}
	if len(f.Instances) > 0 && t.Compute == nil {
		return fmt.Errorf("fixtures declare instances but Target.Compute is nil")
	}
	for _, in := range f.Instances {
		if in.ImageID == "" {
			return fmt.Errorf("instance: imageId is required")
		}
	}
	return nil
}

// Apply validates the whole fixture set, then writes it through t's drivers in
// a fixed order (buckets, tables, secrets, instances). Validation runs first so
// an invalid fixture is rejected before anything is created. Writes are not
// transactional: on a mid-write failure (e.g. seeding a backend that isn't
// empty), earlier resources remain — reset and retry against a fresh backend.
func Apply(ctx context.Context, f Fixtures, t Target) error {
	if err := f.Validate(t); err != nil {
		return err
	}
	if err := applyBuckets(ctx, f.Buckets, t.Storage); err != nil {
		return err
	}
	if err := applyTables(ctx, f.Tables, t.Database); err != nil {
		return err
	}
	if err := applySecrets(ctx, f.Secrets, t.Secrets); err != nil {
		return err
	}
	return applyInstances(ctx, f.Instances, t.Compute)
}

func applyBuckets(ctx context.Context, buckets []Bucket, d storagedriver.Bucket) error {
	for _, b := range buckets {
		if err := d.CreateBucket(ctx, b.Name); err != nil {
			return fmt.Errorf("seed bucket %q: %w", b.Name, err)
		}
		for _, o := range b.Objects {
			ct := o.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			if err := d.PutObject(ctx, b.Name, o.Key, []byte(o.Body), ct, nil); err != nil {
				return fmt.Errorf("seed object %s/%s: %w", b.Name, o.Key, err)
			}
		}
	}
	return nil
}

func applyTables(ctx context.Context, tables []Table, d dbdriver.Database) error {
	for _, tb := range tables {
		if err := d.CreateTable(ctx, dbdriver.TableConfig{
			Name:         tb.Name,
			PartitionKey: tb.PartitionKey,
			SortKey:      tb.SortKey,
		}); err != nil {
			return fmt.Errorf("seed table %q: %w", tb.Name, err)
		}
		for i, item := range tb.Items {
			if err := d.PutItem(ctx, tb.Name, item); err != nil {
				return fmt.Errorf("seed table %q item %d: %w", tb.Name, i, err)
			}
		}
	}
	return nil
}

func applySecrets(ctx context.Context, secrets []Secret, d secretsdriver.Secrets) error {
	for _, s := range secrets {
		if _, err := d.CreateSecret(ctx, secretsdriver.SecretConfig{
			Name:        s.Name,
			Description: s.Description,
		}, []byte(s.Value)); err != nil {
			return fmt.Errorf("seed secret %q: %w", s.Name, err)
		}
	}
	return nil
}

func applyInstances(ctx context.Context, instances []Instance, d computedriver.Compute) error {
	for _, in := range instances {
		count := in.Count
		if count < 1 {
			count = 1
		}
		cfg := computedriver.InstanceConfig{ImageID: in.ImageID, InstanceType: in.InstanceType}
		if in.Name != "" {
			cfg.Tags = map[string]string{"Name": in.Name}
		}
		if _, err := d.RunInstances(ctx, cfg, count); err != nil {
			return fmt.Errorf("seed instance (%s): %w", in.ImageID, err)
		}
	}
	return nil
}

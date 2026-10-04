// Package seed loads declarative fixtures into a cloudemu driver set, so teams
// can check JSON fixtures into their repo and bring the emulator up to a known
// state deterministically.
//
// Fixtures are provider-agnostic: they name resource kinds (buckets, tables,
// secrets, instances), and Apply writes them through the driver interfaces,
// which every provider implements, so the same fixture file seeds AWS, Azure,
// or GCP depending on which drivers you pass in the Target.
package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"unicode"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
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
	IAMUsers  []IAMUser  `json:"iamUsers,omitempty"`
}

// IAMUser is an IAM user plus access keys whose id and secret the fixture sets.
// Under --enforce-auth this is how the first user gets a key: there is no
// signed request to call CreateAccessKey with until one exists. A user with no
// policies is unrestricted, so it can then create everyone else over the API.
type IAMUser struct {
	Name       string      `json:"name"`
	AccessKeys []AccessKey `json:"accessKeys,omitempty"`
}

// AccessKey is a long-term access key with a fixed id and secret.
type AccessKey struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
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
	IAM      iamdriver.IAM
}

// Load parses fixtures from JSON bytes.
func Load(data []byte) (Fixtures, error) {
	var f Fixtures
	if err := json.Unmarshal(data, &f); err != nil {
		return Fixtures{}, fmt.Errorf("parse fixtures: %w", err)
	}
	return f, nil
}

// LoadFS reads and parses a fixture file from fsys. Pass an embed.FS to load
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

	for _, u := range f.IAMUsers {
		n += 1 + len(u.AccessKeys)
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

	return validateIAMUsers(f.IAMUsers, t.IAM)
}

var (
	errNoIAMDriver  = errors.New("fixtures declare iamUsers but Target.IAM is nil")
	errIAMUserName  = errors.New("iamUser: name is required")
	errNoKeyImport  = errors.New("this provider cannot seed access keys")
	errAccessKeyID  = errors.New("accessKeyId must be AKIA followed by 16 uppercase letters or digits, as AWS issues them")
	errAccessSecret = errors.New("secretAccessKey must be 1 to 128 characters with no whitespace")
)

// accessKeyIDPattern is the shape of a long-term AWS access key id.
var accessKeyIDPattern = regexp.MustCompile(`^AKIA[A-Z0-9]{16}$`)

// maxSecretLen bounds a seeded secret; real AWS secrets are 40 characters.
const maxSecretLen = 128

func validSecret(s string) bool {
	return s != "" && len(s) <= maxSecretLen && !strings.ContainsFunc(s, unicode.IsSpace)
}

func validateIAMUsers(users []IAMUser, d iamdriver.IAM) error {
	if len(users) == 0 {
		return nil
	}

	if d == nil {
		return errNoIAMDriver
	}

	_, canImport := d.(iamdriver.AccessKeyImporter)

	for _, u := range users {
		if u.Name == "" {
			return errIAMUserName
		}

		if len(u.AccessKeys) > 0 && !canImport {
			return fmt.Errorf("iamUser %q: %w", u.Name, errNoKeyImport)
		}

		for _, k := range u.AccessKeys {
			if !accessKeyIDPattern.MatchString(k.AccessKeyID) {
				return fmt.Errorf("iamUser %q: accessKeyId %q: %w", u.Name, k.AccessKeyID, errAccessKeyID)
			}

			if !validSecret(k.SecretAccessKey) {
				return fmt.Errorf("iamUser %q, key %s: %w", u.Name, k.AccessKeyID, errAccessSecret)
			}
		}
	}

	return nil
}

// Option configures Apply.
type Option func(*applyOptions)

type applyOptions struct{ ignoreExisting bool }

// IgnoreExisting makes Apply skip a resource that already exists (an
// AlreadyExists error) and continue with the rest of the fixture, instead of
// failing at the first collision. Use it for boot-time init, where a fixture may
// overlap resources already present from restored state or an earlier file, so
// a duplicate skips just that resource rather than truncating everything after
// it in the fixture.
func IgnoreExisting() Option {
	return func(o *applyOptions) { o.ignoreExisting = true }
}

// Apply validates the whole fixture set, then writes it through t's drivers in
// a fixed order (buckets, tables, secrets, instances, IAM users). Validation runs first so
// an invalid fixture is rejected before anything is created. Writes are not
// transactional: on a mid-write failure (e.g. seeding a backend that isn't
// empty), earlier resources remain. Reset and retry against a fresh backend,
// or pass IgnoreExisting to tolerate resources that already exist.
//
//nolint:gocritic // hugeParam: Fixtures is passed by value to keep the stable public Apply signature.
func Apply(ctx context.Context, f Fixtures, t Target, opts ...Option) error {
	var o applyOptions
	for _, opt := range opts {
		opt(&o)
	}

	if err := f.Validate(t); err != nil {
		return err
	}

	if err := applyBuckets(ctx, f.Buckets, t.Storage, o.ignoreExisting); err != nil {
		return err
	}

	if err := applyTables(ctx, f.Tables, t.Database, o.ignoreExisting); err != nil {
		return err
	}

	if err := applySecrets(ctx, f.Secrets, t.Secrets, o.ignoreExisting); err != nil {
		return err
	}

	if err := applyInstances(ctx, f.Instances, t.Compute, o.ignoreExisting); err != nil {
		return err
	}

	return applyIAMUsers(ctx, f.IAMUsers, t.IAM, o.ignoreExisting)
}

func applyIAMUsers(ctx context.Context, users []IAMUser, d iamdriver.IAM, ignoreExisting bool) error {
	for _, u := range users {
		if _, err := d.CreateUser(ctx, iamdriver.UserConfig{Name: u.Name}); err != nil {
			if !ignoreExisting || !cerrors.IsAlreadyExists(err) {
				return fmt.Errorf("seed iam user %q: %w", u.Name, err)
			}
		}

		// Validate already confirmed d is an importer when any keys are declared.
		importer, _ := d.(iamdriver.AccessKeyImporter)

		for _, k := range u.AccessKeys {
			if err := importer.ImportAccessKey(ctx, u.Name, k.AccessKeyID, k.SecretAccessKey); err != nil {
				if !ignoreExisting || !cerrors.IsAlreadyExists(err) {
					return fmt.Errorf("seed access key %q for %q: %w", k.AccessKeyID, u.Name, err)
				}
			}
		}
	}

	return nil
}

func applyBuckets(ctx context.Context, buckets []Bucket, d storagedriver.Bucket, ignoreExisting bool) error {
	for _, b := range buckets {
		if err := d.CreateBucket(ctx, b.Name); err != nil {
			if !ignoreExisting || !cerrors.IsAlreadyExists(err) {
				return fmt.Errorf("seed bucket %q: %w", b.Name, err)
			}
			// Bucket already exists; still (re)put its declared objects below.
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

func applyTables(ctx context.Context, tables []Table, d dbdriver.Database, ignoreExisting bool) error {
	for _, tb := range tables {
		if err := d.CreateTable(ctx, dbdriver.TableConfig{
			Name:         tb.Name,
			PartitionKey: tb.PartitionKey,
			SortKey:      tb.SortKey,
		}); err != nil {
			if !ignoreExisting || !cerrors.IsAlreadyExists(err) {
				return fmt.Errorf("seed table %q: %w", tb.Name, err)
			}
			// Table already exists; still (re)put its declared items below.
		}

		for i, item := range tb.Items {
			if err := d.PutItem(ctx, tb.Name, item); err != nil {
				return fmt.Errorf("seed table %q item %d: %w", tb.Name, i, err)
			}
		}
	}
	return nil
}

func applySecrets(ctx context.Context, secrets []Secret, d secretsdriver.Secrets, ignoreExisting bool) error {
	for _, s := range secrets {
		if _, err := d.CreateSecret(ctx, secretsdriver.SecretConfig{
			Name:        s.Name,
			Description: s.Description,
		}, []byte(s.Value)); err != nil {
			if !ignoreExisting || !cerrors.IsAlreadyExists(err) {
				return fmt.Errorf("seed secret %q: %w", s.Name, err)
			}
		}
	}
	return nil
}

func applyInstances(ctx context.Context, instances []Instance, d computedriver.Compute, ignoreExisting bool) error {
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
			if !ignoreExisting || !cerrors.IsAlreadyExists(err) {
				return fmt.Errorf("seed instance (%s): %w", in.ImageID, err)
			}
		}
	}
	return nil
}

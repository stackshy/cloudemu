// Package blobstore provides an opt-in real object-storage engine that persists
// object bytes to a real local filesystem — no Docker — backing CloudEmu's
// object stores (AWS S3, Azure Blob, GCP GCS). Bytes are written to real files
// under a root directory, so they survive in the store for the process's
// lifetime and can be inspected with ordinary tools. Wire it in with
// config.WithStorageEngine(blobstore.New("")).
//
// It lives in a separate module on purpose: the storage-backing dependency
// stays out of CloudEmu's core. The in-memory provider keeps each object's
// metadata (ETag, versioning, tags); this engine holds only the bytes.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
)

// dirPerm / filePerm are the permissions for created directories and object
// files under the store root.
const (
	dirPerm  = 0o755
	filePerm = 0o644
)

var (
	// errNotFound reports a Get/Copy for an object the store never wrote.
	errNotFound = errors.New("blobstore: object not found")
	// errBadRef reports a bucket/key that would escape the store root.
	errBadRef = errors.New("blobstore: invalid object reference")
)

// Store is a config.StorageEngine backed by a real local filesystem. Safe for
// concurrent use.
type Store struct {
	root    string
	ownRoot bool // true when New created a temp dir → remove it on Close

	mu sync.Mutex
}

// New returns a filesystem-backed StorageEngine rooted at root. An empty root
// creates a temporary directory that Close removes. The directory is created
// lazily on the first write.
func New(root string) *Store {
	if root == "" {
		dir, err := os.MkdirTemp("", "cloudemu-blob-")
		if err == nil {
			return &Store{root: dir, ownRoot: true}
		}
		// Fall back to a fixed path under the OS temp dir if MkdirTemp fails;
		// Put's MkdirAll will surface any real error.
		root = filepath.Join(os.TempDir(), "cloudemu-blob")
	}

	return &Store{root: root}
}

// Put writes the object's bytes to a real file under the store root.
//
//nolint:gocritic // obj is the by-value DTO defined by the StorageEngine contract
func (s *Store) Put(_ context.Context, obj config.StorageObject) error {
	path, err := s.pathFor(config.StorageRef{Bucket: obj.Bucket, Key: obj.Key, Version: obj.Version})
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}

	return os.WriteFile(path, obj.Data, filePerm)
}

// Get reads the object's bytes back. A missing object is a not-found error.
func (s *Store) Get(_ context.Context, ref config.StorageRef) (config.StorageObject, error) {
	path, err := s.pathFor(ref)
	if err != nil {
		return config.StorageObject{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.StorageObject{}, fmt.Errorf("%w: %s/%s", errNotFound, ref.Bucket, ref.Key)
	}

	if err != nil {
		return config.StorageObject{}, err
	}

	return config.StorageObject{Bucket: ref.Bucket, Key: ref.Key, Version: ref.Version, Data: data}, nil
}

// Delete removes the object's bytes. A missing object is a no-op (idempotent
// deletion, matching object-storage semantics).
func (s *Store) Delete(_ context.Context, ref config.StorageRef) error {
	path, err := s.pathFor(ref)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

// Copy duplicates src's bytes to dst server-side.
func (s *Store) Copy(ctx context.Context, dst, src config.StorageRef) error {
	obj, err := s.Get(ctx, src)
	if err != nil {
		return err
	}

	return s.Put(ctx, config.StorageObject{Bucket: dst.Bucket, Key: dst.Key, Version: dst.Version, Data: obj.Data})
}

// Close removes the store root when New created it. Safe to call more than once.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.ownRoot {
		return nil
	}

	s.ownRoot = false

	return os.RemoveAll(s.root)
}

// Root returns the directory bytes are written under (useful for tests that
// inspect the real files).
func (s *Store) Root() string { return s.root }

// pathFor maps a bucket/key/version to a real path under the root, rejecting
// any reference whose cleaned path would escape the root (S3 keys are arbitrary
// and may contain "..").
func (s *Store) pathFor(ref config.StorageRef) (string, error) {
	if ref.Bucket == "" || ref.Key == "" {
		return "", errBadRef
	}

	// Namespace current objects and versions separately so a key literally named
	// like a version can never collide with a versioned object.
	sub := "current"
	parts := []string{s.root, "buckets", ref.Bucket, sub, ref.Key}

	if ref.Version != "" {
		parts = []string{s.root, "buckets", ref.Bucket, "versions", ref.Key, ref.Version}
	}

	path := filepath.Join(parts...)

	// Guard against traversal: the cleaned path must stay under the root.
	root := filepath.Clean(s.root)
	if path != root && !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %s/%s", errBadRef, ref.Bucket, ref.Key)
	}

	return path, nil
}

var _ config.StorageEngine = (*Store)(nil)

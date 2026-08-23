// Package storageengine wires an optional real storage engine into an
// object-storage provider's data path. It is shared by every storage provider
// (AWS S3, Azure Blob, GCP GCS) so the put/get/delete/copy hook stays identical
// across clouds and cannot drift. When no engine is configured every call is a
// no-op, leaving the provider's in-memory object bytes untouched.
package storageengine

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Put persists the object's bytes to the engine when one is configured. No-op
// when engine is nil, leaving the in-memory bytes as the source of truth.
//
//nolint:gocritic // obj is the by-value DTO defined by the StorageEngine contract
func Put(ctx context.Context, engine config.StorageEngine, obj config.StorageObject) error {
	if engine == nil {
		return nil
	}

	if err := engine.Put(ctx, obj); err != nil {
		return cerrors.Newf(cerrors.Internal, "put storage engine: %v", err)
	}

	return nil
}

// Get returns the object's bytes from the engine. ok is false (with a nil
// error) when no engine is wired, so callers fall through to the in-memory copy
// exactly like the nil no-op.
func Get(ctx context.Context, engine config.StorageEngine, ref config.StorageRef) (data []byte, ok bool, err error) {
	if engine == nil {
		return nil, false, nil
	}

	obj, err := engine.Get(ctx, ref)
	if err != nil {
		return nil, false, cerrors.Newf(cerrors.Internal, "get storage engine: %v", err)
	}

	return obj.Data, true, nil
}

// Delete removes the object's bytes from the engine, if any. No-op when engine
// is nil.
func Delete(ctx context.Context, engine config.StorageEngine, ref config.StorageRef) error {
	if engine == nil {
		return nil
	}

	if err := engine.Delete(ctx, ref); err != nil {
		return cerrors.Newf(cerrors.Internal, "delete storage engine: %v", err)
	}

	return nil
}

// Copy duplicates src's bytes to dst in the engine, if one is configured. No-op
// when engine is nil.
func Copy(ctx context.Context, engine config.StorageEngine, dst, src config.StorageRef) error {
	if engine == nil {
		return nil
	}

	if err := engine.Copy(ctx, dst, src); err != nil {
		return cerrors.Newf(cerrors.Internal, "copy storage engine: %v", err)
	}

	return nil
}

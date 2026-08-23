package storageengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

// fakeEngine is an in-memory config.StorageEngine for exercising the helper.
type fakeEngine struct {
	objs   map[string][]byte
	failOn string // op name to fail, for the error-wrapping cases
}

func newFake() *fakeEngine { return &fakeEngine{objs: map[string][]byte{}} }

func key(r config.StorageRef) string { return r.Bucket + "/" + r.Key + "/" + r.Version }

func (f *fakeEngine) Put(_ context.Context, obj config.StorageObject) error {
	if f.failOn == "put" {
		return errors.New("boom")
	}

	f.objs[key(config.StorageRef{Bucket: obj.Bucket, Key: obj.Key, Version: obj.Version})] = obj.Data

	return nil
}

func (f *fakeEngine) Get(_ context.Context, ref config.StorageRef) (config.StorageObject, error) {
	if f.failOn == "get" {
		return config.StorageObject{}, errors.New("boom")
	}

	return config.StorageObject{Bucket: ref.Bucket, Key: ref.Key, Version: ref.Version, Data: f.objs[key(ref)]}, nil
}

func (f *fakeEngine) Delete(_ context.Context, ref config.StorageRef) error {
	if f.failOn == "delete" {
		return errors.New("boom")
	}

	delete(f.objs, key(ref))

	return nil
}

func (f *fakeEngine) Copy(ctx context.Context, dst, src config.StorageRef) error {
	if f.failOn == "copy" {
		return errors.New("boom")
	}

	obj, _ := f.Get(ctx, src)

	return f.Put(ctx, config.StorageObject{Bucket: dst.Bucket, Key: dst.Key, Version: dst.Version, Data: obj.Data})
}

func TestNilEngineIsNoOp(t *testing.T) {
	ctx := context.Background()
	ref := config.StorageRef{Bucket: "b", Key: "k"}

	require.NoError(t, storageengine.Put(ctx, nil, config.StorageObject{Bucket: "b", Key: "k", Data: []byte("x")}))
	require.NoError(t, storageengine.Delete(ctx, nil, ref))
	require.NoError(t, storageengine.Copy(ctx, nil, ref, ref))

	data, ok, err := storageengine.Get(ctx, nil, ref)
	require.NoError(t, err)
	assert.False(t, ok, "no engine → ok is false so the caller falls back to memory")
	assert.Nil(t, data)
}

func TestRoundTripThroughEngine(t *testing.T) {
	ctx := context.Background()
	eng := newFake()
	ref := config.StorageRef{Bucket: "b", Key: "k"}

	require.NoError(t, storageengine.Put(ctx, eng, config.StorageObject{Bucket: "b", Key: "k", Data: []byte("hello")}))

	data, ok, err := storageengine.Get(ctx, eng, ref)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []byte("hello"), data)

	dst := config.StorageRef{Bucket: "b", Key: "k2"}
	require.NoError(t, storageengine.Copy(ctx, eng, dst, ref))
	got, _, _ := storageengine.Get(ctx, eng, dst)
	assert.Equal(t, []byte("hello"), got)

	require.NoError(t, storageengine.Delete(ctx, eng, ref))
	gone, _, _ := storageengine.Get(ctx, eng, ref)
	assert.Nil(t, gone)
}

func TestEngineErrorsAreWrapped(t *testing.T) {
	ctx := context.Background()
	ref := config.StorageRef{Bucket: "b", Key: "k"}

	assert.Error(t, storageengine.Put(ctx, &fakeEngine{objs: map[string][]byte{}, failOn: "put"}, config.StorageObject{Bucket: "b", Key: "k"}))
	_, _, err := storageengine.Get(ctx, &fakeEngine{objs: map[string][]byte{}, failOn: "get"}, ref)
	assert.Error(t, err)
	assert.Error(t, storageengine.Delete(ctx, &fakeEngine{objs: map[string][]byte{}, failOn: "delete"}, ref))
	assert.Error(t, storageengine.Copy(ctx, &fakeEngine{objs: map[string][]byte{}, failOn: "copy"}, ref, ref))
}

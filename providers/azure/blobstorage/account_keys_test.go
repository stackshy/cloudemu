package blobstorage

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListStorageAccountKeys(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	keys, err := m.ListStorageAccountKeys(ctx, "acct1")
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, "key1", keys[0].KeyName)
	assert.Equal(t, "key2", keys[1].KeyName)
	assert.NotEmpty(t, keys[0].Value)
	assert.Equal(t, "Full", keys[0].Permissions)

	// Keys are stable across calls.
	again, err := m.ListStorageAccountKeys(ctx, "acct1")
	require.NoError(t, err)
	assert.Equal(t, keys[0].Value, again[0].Value)
}

func TestRegenerateStorageAccountKey(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	before, err := m.ListStorageAccountKeys(ctx, "acct1")
	require.NoError(t, err)

	after, err := m.RegenerateStorageAccountKey(ctx, "acct1", "key1")
	require.NoError(t, err)
	require.Len(t, after, 2)
	assert.NotEqual(t, before[0].Value, after[0].Value, "key1 rotates")
	assert.Equal(t, before[1].Value, after[1].Value, "key2 unchanged")
}

func TestRegenerateStorageAccountKeyInvalidName(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.RegenerateStorageAccountKey(ctx, "acct1", "key9")
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

package vpc

import (
	"context"
	"encoding/json"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addressLabelsOf(t *testing.T, a *driver.GCPAddress) (map[string]string, string) {
	t.Helper()

	var body struct {
		Labels           map[string]string `json:"labels"`
		LabelFingerprint string            `json:"labelFingerprint"`
	}

	require.NoError(t, json.Unmarshal(a.Body, &body))

	return body.Labels, body.LabelFingerprint
}

// TestGCPAddressStoreLifecycle drives the reserved-address capability directly
// (the Go library path): insert stamps a fingerprint, duplicates and absent
// names are refused, setLabels enforces the fingerprint, lists filter by
// project and scope, and delete removes the record.
func TestGCPAddressStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insert := func(scope, name, body string) error {
		return m.InsertGCPAddress(ctx, driver.GCPAddress{Project: "p", Scope: scope, Name: name, Body: json.RawMessage(body)})
	}

	require.NoError(t, insert("global", "a", `{"name":"a","labels":{"team":"net"}}`))
	require.NoError(t, insert("us-central1", "a", `{"name":"a"}`))
	require.NoError(t, m.InsertGCPAddress(ctx, driver.GCPAddress{Project: "q", Scope: "global", Name: "z", Body: json.RawMessage(`{"name":"z"}`)}))

	assert.True(t, cerrors.IsAlreadyExists(insert("global", "a", `{"name":"a"}`)))
	assert.True(t, cerrors.IsInvalidArgument(insert("global", "bad", `[1]`)))

	got, err := m.GetGCPAddress(ctx, "p", "global", "a")
	require.NoError(t, err)

	labels, fp := addressLabelsOf(t, got)
	assert.Equal(t, map[string]string{"team": "net"}, labels)
	assert.Equal(t, addressLabelFingerprint(labels), fp)

	_, err = m.GetGCPAddress(ctx, "p", "global", "nope")
	assert.True(t, cerrors.IsNotFound(err))

	assert.True(t, cerrors.IsFailedPrecondition(m.SetGCPAddressLabels(ctx, "p", "global", "a", nil, "")))
	assert.True(t, cerrors.IsFailedPrecondition(m.SetGCPAddressLabels(ctx, "p", "global", "a", nil, "stale")))
	assert.True(t, cerrors.IsNotFound(m.SetGCPAddressLabels(ctx, "p", "global", "nope", nil, fp)))

	require.NoError(t, m.SetGCPAddressLabels(ctx, "p", "global", "a", map[string]string{"env": "prod"}, fp))

	got, err = m.GetGCPAddress(ctx, "p", "global", "a")
	require.NoError(t, err)

	labels, fp2 := addressLabelsOf(t, got)
	assert.Equal(t, map[string]string{"env": "prod"}, labels)
	assert.NotEqual(t, fp, fp2)

	require.NoError(t, m.SetGCPAddressLabels(ctx, "p", "global", "a", nil, fp2))

	got, err = m.GetGCPAddress(ctx, "p", "global", "a")
	require.NoError(t, err)

	labels, _ = addressLabelsOf(t, got)
	assert.Empty(t, labels, "an empty setLabels removes every label")

	all, err := m.ListGCPAddresses(ctx, "p", "")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "global", all[0].Scope)
	assert.Equal(t, "us-central1", all[1].Scope)

	regional, err := m.ListGCPAddresses(ctx, "p", "us-central1")
	require.NoError(t, err)
	assert.Len(t, regional, 1)

	require.NoError(t, m.DeleteGCPAddress(ctx, "p", "global", "a"))
	assert.True(t, cerrors.IsNotFound(m.DeleteGCPAddress(ctx, "p", "global", "a")))
}

// TestGCPAddressStoreSnapshot: addresses and the IP allocator are part of the
// provider snapshot, and a returned body never aliases the stored one.
func TestGCPAddressStoreSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	ip1, err := src.AllocateGCPAddressIP(ctx)
	require.NoError(t, err)
	assert.Equal(t, "10.128.0.1", ip1)

	require.NoError(t, src.InsertGCPAddress(ctx, driver.GCPAddress{
		Project: "p", Scope: "global", Name: "a", Body: json.RawMessage(`{"name":"a","address":"10.128.0.1"}`),
	}))

	got, err := src.GetGCPAddress(ctx, "p", "global", "a")
	require.NoError(t, err)

	got.Body[0] = 'X'

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	restored, err := dst.GetGCPAddress(ctx, "p", "global", "a")
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"a","address":"10.128.0.1","labelFingerprint":"`+addressLabelFingerprint(nil)+`"}`,
		string(restored.Body))

	ip2, err := dst.AllocateGCPAddressIP(ctx)
	require.NoError(t, err)
	assert.Equal(t, "10.128.0.2", ip2)
}

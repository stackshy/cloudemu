package loadbalancer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

const (
	testSARegion  = "us-central1"
	consumerNet   = "projects/consumer/global/networks/vpc"
	consumerEPURL = "https://compute.googleapis.com/compute/v1/projects/consumer/regions/us-central1/forwardingRules/"
)

func saBody(pref string, extra map[string]any) map[string]any {
	body := map[string]any{
		"connectionPreference": pref,
		"targetService":        "projects/p/regions/us-central1/forwardingRules/ilb",
		"natSubnets":           []any{"projects/p/regions/us-central1/subnetworks/nat"},
	}

	for k, v := range extra {
		body[k] = v
	}

	return body
}

func endpoint(name string) driver.GCPPSCEndpoint {
	return driver.GCPPSCEndpoint{Endpoint: consumerEPURL + name, PscConnectionID: name, ConsumerNetwork: consumerNet}
}

// TestGCPServiceAttachmentStoreLibrary drives the capability directly (the Go
// library path): validation, CRUD, connection decisions by project, network
// and endpoint URL, a mutate error leaving the record untouched, and a
// snapshot round trip that keeps connectedEndpoints.
func TestGCPServiceAttachmentStoreLibrary(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insert := func(name string, body map[string]any) error {
		return m.InsertGCPServiceAttachment(ctx, driver.GCPResource{Scope: testSARegion, Name: name, ID: "1", Body: body})
	}

	for name, body := range map[string]map[string]any{
		"preference": saBody("SOMETIMES", nil),
		"target":     saBody(driver.PSCAcceptAutomatic, map[string]any{"targetService": ""}),
		"nat":        saBody(driver.PSCAcceptAutomatic, map[string]any{"natSubnets": []any{}}),
		"entry":      saBody(driver.PSCAcceptManual, map[string]any{"consumerAcceptLists": []any{map[string]any{}}}),
		"limit": saBody(driver.PSCAcceptManual, map[string]any{"consumerAcceptLists": []any{
			map[string]any{"projectIdOrNum": "consumer", "connectionLimit": -1.0},
		}}),
	} {
		assert.True(t, cerrors.IsInvalidArgument(insert("bad", body)), name)
	}

	require.NoError(t, insert("by-network", saBody(driver.PSCAcceptManual, map[string]any{
		"consumerAcceptLists": []any{map[string]any{"networkUrl": "https://x/compute/v1/" + consumerNet}},
	})))
	require.NoError(t, insert("by-endpoint", saBody(driver.PSCAcceptManual, map[string]any{
		"consumerAcceptLists": []any{map[string]any{"endpointUrl": consumerEPURL + "ep-b", "connectionLimit": "1"}},
	})))
	require.NoError(t, insert("reject-net", saBody(driver.PSCAcceptManual, map[string]any{
		"consumerRejectLists": []any{consumerNet},
		// a client-sent connectedEndpoints is output-only and dropped.
		"connectedEndpoints": []any{map[string]any{"pscConnectionId": "forged"}},
	})))
	assert.True(t, cerrors.IsAlreadyExists(insert("by-network", saBody(driver.PSCAcceptAutomatic, nil))))

	for _, tc := range []struct{ sa, ep, want string }{
		{"by-network", "ep-a", driver.PSCStatusAccepted},
		{"by-endpoint", "ep-b", driver.PSCStatusAccepted},
		{"by-endpoint", "ep-c", driver.PSCStatusPending},
		{"reject-net", "ep-d", driver.PSCStatusRejected},
	} {
		got, err := m.ConnectGCPServiceAttachment(ctx, testSARegion, tc.sa, endpoint(tc.ep))
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, tc.sa+"/"+tc.ep)
		assert.Equal(t, tc.want, m.GCPPSCConnectionStatus(ctx, testSARegion, tc.sa, tc.ep))
	}

	_, err := m.ConnectGCPServiceAttachment(ctx, testSARegion, "ghost", endpoint("x"))
	assert.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, driver.PSCStatusClosed, m.GCPPSCConnectionStatus(ctx, testSARegion, "ghost", "x"))
	assert.Equal(t, driver.PSCStatusClosed, m.GCPPSCConnectionStatus(ctx, testSARegion, "by-network", "unknown"))

	rejected, err := m.GetGCPServiceAttachment(ctx, testSARegion, "reject-net")
	require.NoError(t, err)
	assert.Len(t, rejected.Body["connectedEndpoints"], 1, "the forged endpoint was dropped")

	errReject := cerrors.New(cerrors.InvalidArgument, "rejected")
	err = m.UpdateGCPServiceAttachment(ctx, testSARegion, "by-network", func(res *driver.GCPResource) error {
		res.Body["connectionPreference"] = driver.PSCAcceptAutomatic
		return errReject
	})
	require.ErrorIs(t, err, errReject)

	got, err := m.GetGCPServiceAttachment(ctx, testSARegion, "by-network")
	require.NoError(t, err)
	assert.Equal(t, driver.PSCAcceptManual, got.Body["connectionPreference"])

	// A successful update keeps connectedEndpoints (output-only) even when the
	// mutation replaces the body, and re-evaluates them.
	require.NoError(t, m.UpdateGCPServiceAttachment(ctx, testSARegion, "by-network", func(res *driver.GCPResource) error {
		res.Body = saBody(driver.PSCAcceptManual, map[string]any{"consumerRejectLists": []any{"consumer"}})
		return nil
	}))
	assert.Equal(t, driver.PSCStatusRejected, m.GCPPSCConnectionStatus(ctx, testSARegion, "by-network", "ep-a"))

	assert.True(t, cerrors.IsInvalidArgument(m.UpdateGCPServiceAttachment(ctx, testSARegion, "by-network",
		func(res *driver.GCPResource) error {
			res.Body["natSubnets"] = nil
			return nil
		})))

	require.NoError(t, insert("by-project", saBody(driver.PSCAcceptManual, map[string]any{
		"consumerAcceptLists": []any{map[string]any{"projectIdOrNum": "consumer", "connectionLimit": 1.0}},
	})))

	for _, ep := range []string{"ep-p1", "ep-p2"} {
		_, err := m.ConnectGCPServiceAttachment(ctx, testSARegion, "by-project", endpoint(ep))
		require.NoError(t, err)
	}

	assert.Equal(t, driver.PSCStatusPending, m.GCPPSCConnectionStatus(ctx, testSARegion, "by-project", "ep-p2"))
	require.NoError(t, m.DisconnectGCPServiceAttachment(ctx, testSARegion, "by-project", "ep-p1"))
	assert.Equal(t, driver.PSCStatusAccepted, m.GCPPSCConnectionStatus(ctx, testSARegion, "by-project", "ep-p2"),
		"a freed connection slot is re-evaluated")
	require.NoError(t, m.DisconnectGCPServiceAttachment(ctx, testSARegion, "ghost", "x"))

	items, err := m.ListGCPServiceAttachments(ctx, testSARegion)
	require.NoError(t, err)
	assert.Len(t, items, 4)

	data, err := m.Snapshot(ctx, false)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))
	assert.Equal(t, driver.PSCStatusRejected, restored.GCPPSCConnectionStatus(ctx, testSARegion, "by-network", "ep-a"))

	require.NoError(t, m.DeleteGCPServiceAttachment(ctx, testSARegion, "by-network"))
	assert.True(t, cerrors.IsNotFound(m.DeleteGCPServiceAttachment(ctx, testSARegion, "by-network")))
	assert.True(t, cerrors.IsNotFound(m.UpdateGCPServiceAttachment(ctx, testSARegion, "by-network",
		func(*driver.GCPResource) error { return nil })))

	_, err = m.GetGCPServiceAttachment(ctx, testSARegion, "by-network")
	assert.True(t, cerrors.IsNotFound(err))
}

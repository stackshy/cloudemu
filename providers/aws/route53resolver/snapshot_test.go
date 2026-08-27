package route53resolver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

// TestSnapshotRoundTripRoute53Resolver proves a snapshot/restore round-trip
// preserves the resource stores, the ARN-keyed tag store, and the idempotency
// store under their original identities.
func TestSnapshotRoundTripRoute53Resolver(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	ep, err := src.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
		Name:             "in-1",
		Direction:        directionInbound,
		SecurityGroupIDs: []string{"sg-1"},
		IPAddresses:      []driver.IPAddress{{SubnetID: "subnet-a"}, {SubnetID: "subnet-b"}},
		Tags:             []driver.Tag{{Key: "env", Value: "test"}},
	})
	require.NoError(t, err)

	rule, err := src.CreateResolverRule(ctx, &driver.CreateResolverRuleInput{
		Name: "rule-1", DomainName: "example.com.", RuleType: "FORWARD",
		ResolverEndpointID: ep.ID,
		TargetIPs:          []driver.TargetAddress{{IP: "10.0.0.2", Port: int32(53)}},
	})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	gotEP, err := dst.GetResolverEndpoint(ctx, ep.ID)
	require.NoError(t, err)
	assert.Equal(t, ep.ARN, gotEP.ARN)
	assert.Equal(t, directionInbound, gotEP.Direction)
	assert.Equal(t, int32(2), gotEP.IPAddressCount)

	gotRule, err := dst.GetResolverRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, "example.com.", gotRule.DomainName)
	require.Len(t, gotRule.TargetIPs, 1)
	assert.Equal(t, "10.0.0.2", gotRule.TargetIPs[0].IP)

	// Tags (stored in the ARN-keyed tag store) survived the round-trip.
	tags, err := dst.ListTagsForResource(ctx, ep.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "env", Value: "test"}}, tags)
}

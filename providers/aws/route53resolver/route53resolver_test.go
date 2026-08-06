package route53resolver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func ptr[T any](v T) *T { return &v }

// ---- resolver endpoints ----

func TestEndpointCreateGetUpdateDeleteAndIPs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ep, err := m.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
		Name:             "in-1",
		Direction:        directionInbound,
		SecurityGroupIDs: []string{"sg-1"},
		IPAddresses:      []driver.IPAddress{{SubnetID: "subnet-a"}, {SubnetID: "subnet-b"}},
		Tags:             []driver.Tag{{Key: "env", Value: "test"}},
	})
	require.NoError(t, err)
	assert.Contains(t, ep.ID, "rslvr-in-")
	assert.Equal(t, int32(2), ep.IPAddressCount)
	assert.Equal(t, statusOperational, ep.Status)

	// Tags stored on create are retrievable by ARN.
	tags, err := m.ListTagsForResource(ctx, ep.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "env", Value: "test"}}, tags)

	// Add an IP → count grows; remove → count shrinks.
	afterAdd, err := m.AssociateResolverEndpointIPAddress(ctx, ep.ID, &driver.IPAddress{SubnetID: "subnet-c"})
	require.NoError(t, err)
	assert.Equal(t, int32(3), afterAdd.IPAddressCount)

	ips, err := m.ListResolverEndpointIPAddresses(ctx, ep.ID)
	require.NoError(t, err)
	assert.Len(t, ips, 3)
	assert.NotEmpty(t, ips[0].IPID)

	afterDel, err := m.DisassociateResolverEndpointIPAddress(ctx, ep.ID, &driver.IPAddress{IPID: ips[0].IPID})
	require.NoError(t, err)
	assert.Equal(t, int32(2), afterDel.IPAddressCount)

	upd, err := m.UpdateResolverEndpoint(ctx, ep.ID, driver.UpdateResolverEndpointInput{Name: ptr("in-renamed")})
	require.NoError(t, err)
	assert.Equal(t, "in-renamed", upd.Name)

	del, err := m.DeleteResolverEndpoint(ctx, ep.ID)
	require.NoError(t, err)
	assert.Equal(t, statusDeleting, del.Status)

	_, err = m.GetResolverEndpoint(ctx, ep.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestEndpointErrorPaths(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.GetResolverEndpoint(ctx, "rslvr-in-missing")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.UpdateResolverEndpoint(ctx, "nope", driver.UpdateResolverEndpointInput{Name: ptr("x")})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.DeleteResolverEndpoint(ctx, "nope")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.AssociateResolverEndpointIPAddress(ctx, "nope", &driver.IPAddress{SubnetID: "s"})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.ListResolverEndpointIPAddresses(ctx, "nope")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestEndpointListSortedAndCloneIsolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		_, err := m.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
			Name: name, Direction: directionInbound,
			IPAddresses: []driver.IPAddress{{SubnetID: "s"}},
		})
		require.NoError(t, err)
	}

	list, err := m.ListResolverEndpoints(ctx)
	require.NoError(t, err)
	require.Len(t, list, 3)

	// Mutating a returned copy must not corrupt the stored record.
	list[0].Name = "MUTATED"
	list[0].IPAddresses[0].IP = "9.9.9.9"

	reread, err := m.ListResolverEndpoints(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, "MUTATED", reread[0].Name)
	assert.NotEqual(t, "9.9.9.9", reread[0].IPAddresses[0].IP)
}

// ---- resolver rules ----

func TestRuleLifecycleAssociationsAndPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	rule, err := m.CreateResolverRule(ctx, &driver.CreateResolverRuleInput{
		Name: "fwd", RuleType: "FORWARD", DomainName: "example.com",
		TargetIPs: []driver.TargetAddress{{IP: "10.0.0.2", Port: 53}},
	})
	require.NoError(t, err)
	assert.Contains(t, rule.ID, "rslvr-rr-")

	upd, err := m.UpdateResolverRule(ctx, rule.ID, driver.UpdateResolverRuleInput{Name: ptr("fwd2")})
	require.NoError(t, err)
	assert.Equal(t, "fwd2", upd.Name)

	assoc, err := m.AssociateResolverRule(ctx, rule.ID, "vpc-1", "assoc-1")
	require.NoError(t, err)
	assert.Contains(t, assoc.ID, "rslvr-rrassoc-")

	got, err := m.GetResolverRuleAssociation(ctx, assoc.ID)
	require.NoError(t, err)
	assert.Equal(t, "vpc-1", got.VPCID)

	assocs, err := m.ListResolverRuleAssociations(ctx)
	require.NoError(t, err)
	assert.Len(t, assocs, 1)

	dis, err := m.DisassociateResolverRule(ctx, rule.ID, "vpc-1")
	require.NoError(t, err)
	assert.Equal(t, assoc.ID, dis.ID)

	require.NoError(t, m.PutResolverRulePolicy(ctx, rule.ARN, `{"policy":true}`))
	pol, err := m.GetResolverRulePolicy(ctx, rule.ARN)
	require.NoError(t, err)
	assert.Equal(t, `{"policy":true}`, pol)

	del, err := m.DeleteResolverRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, rule.ID, del.ID)

	_, err = m.GetResolverRule(ctx, rule.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestRuleErrorPaths(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.GetResolverRule(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.UpdateResolverRule(ctx, "missing", driver.UpdateResolverRuleInput{})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.AssociateResolverRule(ctx, "missing", "vpc-1", "n")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.DisassociateResolverRule(ctx, "missing", "vpc-1")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetResolverRuleAssociation(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))

	// Empty policy read is not an error, just empty.
	pol, err := m.GetResolverRulePolicy(ctx, "arn:none")
	require.NoError(t, err)
	assert.Empty(t, pol)
}

// ---- query-log configs ----

func TestQueryLogConfigLifecycleAndAssocCount(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	qlc, err := m.CreateResolverQueryLogConfig(ctx, &driver.CreateQueryLogConfigInput{
		Name: "logs", DestinationARN: "arn:aws:s3:::bucket",
	})
	require.NoError(t, err)
	assert.Contains(t, qlc.ID, "rqlc-")

	a1, err := m.AssociateResolverQueryLogConfig(ctx, qlc.ID, "vpc-1")
	require.NoError(t, err)
	_, err = m.AssociateResolverQueryLogConfig(ctx, qlc.ID, "vpc-2")
	require.NoError(t, err)

	got, err := m.GetResolverQueryLogConfig(ctx, qlc.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2), got.AssociationCount)

	list, err := m.ListResolverQueryLogConfigs(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int32(2), list[0].AssociationCount)

	gotAssoc, err := m.GetResolverQueryLogConfigAssociation(ctx, a1.ID)
	require.NoError(t, err)
	assert.Equal(t, "vpc-1", gotAssoc.ResourceID)

	_, err = m.DisassociateResolverQueryLogConfig(ctx, qlc.ID, "vpc-1")
	require.NoError(t, err)

	got, err = m.GetResolverQueryLogConfig(ctx, qlc.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), got.AssociationCount)

	del, err := m.DeleteResolverQueryLogConfig(ctx, qlc.ID)
	require.NoError(t, err)
	assert.Equal(t, qlcStatusDeleting, del.Status)
}

func TestQueryLogConfigErrorPaths(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.GetResolverQueryLogConfig(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.DeleteResolverQueryLogConfig(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.AssociateResolverQueryLogConfig(ctx, "missing", "vpc-1")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.DisassociateResolverQueryLogConfig(ctx, "missing", "vpc-1")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetResolverQueryLogConfigAssociation(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))
}

// ---- resolver & DNSSEC configs ----

func TestResolverConfigLazyDefaultAndUpdate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Nothing listed until a VPC is touched.
	list, err := m.ListResolverConfigs(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)

	// A pure Get returns the default but must NOT persist a phantom config.
	got, err := m.GetResolverConfig(ctx, "vpc-1")
	require.NoError(t, err)
	assert.Equal(t, autodefinedReverseEnabled, got.AutodefinedReverse)

	list, err = m.ListResolverConfigs(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)

	// Update materializes it; now it appears in the List.
	_, err = m.UpdateResolverConfig(ctx, "vpc-1", flagEnable)
	require.NoError(t, err)

	list, err = m.ListResolverConfigs(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	for flag, want := range map[string]string{
		"DISABLE":                    autodefinedReverseDisabled,
		flagEnable:                   autodefinedReverseEnabled,
		"USE_LOCAL_RESOURCE_SETTING": autodefinedReverseLocal,
	} {
		upd, uerr := m.UpdateResolverConfig(ctx, "vpc-1", flag)
		require.NoError(t, uerr)
		assert.Equal(t, want, upd.AutodefinedReverse)
	}
}

func TestDnssecConfigLazyDefaultAndUpdate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	got, err := m.GetResolverDnssecConfig(ctx, "vpc-1")
	require.NoError(t, err)
	assert.Equal(t, dnssecStatusDisabled, got.ValidationStatus)

	upd, err := m.UpdateResolverDnssecConfig(ctx, "vpc-1", flagEnable)
	require.NoError(t, err)
	assert.Equal(t, dnssecStatusEnabled, upd.ValidationStatus)

	list, err := m.ListResolverDnssecConfigs(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// ---- DNS firewall ----

func TestFirewallDomainOps(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	dl, err := m.CreateFirewallDomainList(ctx, "req", "block", nil)
	require.NoError(t, err)
	assert.Contains(t, dl.ID, "rslvr-fdl-")

	_, err = m.UpdateFirewallDomains(ctx, dl.ID, domainOpAdd, []string{"a.com", "b.com", "a.com"})
	require.NoError(t, err)
	domains, err := m.ListFirewallDomains(ctx, dl.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.com", "b.com"}, domains) // dedup on ADD

	_, err = m.UpdateFirewallDomains(ctx, dl.ID, domainOpRemove, []string{"a.com"})
	require.NoError(t, err)
	domains, err = m.ListFirewallDomains(ctx, dl.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.com"}, domains)

	_, err = m.UpdateFirewallDomains(ctx, dl.ID, domainOpReplace, []string{"x.com", "y.com"})
	require.NoError(t, err)
	domains, err = m.ListFirewallDomains(ctx, dl.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"x.com", "y.com"}, domains)

	got, err := m.GetFirewallDomainList(ctx, dl.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2), got.DomainCount)

	_, err = m.ImportFirewallDomains(ctx, dl.ID, domainOpReplace, "s3://x")
	require.NoError(t, err)

	del, err := m.DeleteFirewallDomainList(ctx, dl.ID)
	require.NoError(t, err)
	assert.Equal(t, fwStatusDeleting, del.Status)

	_, err = m.GetFirewallDomainList(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))
	_, err = m.UpdateFirewallDomains(ctx, "missing", domainOpAdd, []string{"a"})
	assert.True(t, cerrors.IsNotFound(err))
	_, err = m.ListFirewallDomains(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestFirewallRulesBatchAndCascade(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	rg, err := m.CreateFirewallRuleGroup(ctx, "req", "rg", nil)
	require.NoError(t, err)

	// Rule against a nonexistent group fails.
	_, err = m.CreateFirewallRule(ctx, &driver.FirewallRuleInput{FirewallRuleGroupID: "missing", FirewallDomainListID: "dl"})
	assert.True(t, cerrors.IsNotFound(err))

	created, err := m.BatchCreateFirewallRules(ctx, []driver.FirewallRuleInput{
		{FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-1", Priority: 20, Action: "BLOCK"},
		{FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-2", Priority: 10, Action: "ALLOW"},
	})
	require.NoError(t, err)
	assert.Len(t, created, 2)

	// ListFirewallRules is sorted by priority ascending.
	rules, err := m.ListFirewallRules(ctx, rg.ID)
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, int32(10), rules[0].Priority)

	got, err := m.GetFirewallRuleGroup(ctx, rg.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2), got.RuleCount)

	// Update preserves creation time, mutates action.
	upd, err := m.BatchUpdateFirewallRules(ctx, []driver.FirewallRuleInput{
		{FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-1", Action: "ALERT", Priority: 20},
	})
	require.NoError(t, err)
	assert.Equal(t, "ALERT", upd[0].Action)

	// Updating a nonexistent rule errors.
	_, err = m.UpdateFirewallRule(ctx, &driver.FirewallRuleInput{FirewallRuleGroupID: rg.ID, FirewallDomainListID: "nope"})
	assert.True(t, cerrors.IsNotFound(err))

	deleted, err := m.BatchDeleteFirewallRules(ctx, rg.ID, []driver.FirewallRuleKey{{FirewallDomainListID: "dl-1"}})
	require.NoError(t, err)
	assert.Len(t, deleted, 1)

	got, err = m.GetFirewallRuleGroup(ctx, rg.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(1), got.RuleCount)

	// Deleting the group cascades to remaining rules.
	_, err = m.DeleteFirewallRuleGroup(ctx, rg.ID)
	require.NoError(t, err)
	rules, err = m.ListFirewallRules(ctx, rg.ID)
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestFirewallAssociationsConfigAndPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	rg, err := m.CreateFirewallRuleGroup(ctx, "req", "rg", nil)
	require.NoError(t, err)

	require.NoError(t, m.PutFirewallRuleGroupPolicy(ctx, rg.ARN, "pol"))
	pol, err := m.GetFirewallRuleGroupPolicy(ctx, rg.ARN)
	require.NoError(t, err)
	assert.Equal(t, "pol", pol)

	// Associate against a missing group errors.
	_, err = m.AssociateFirewallRuleGroup(ctx, &driver.AssociateFirewallRuleGroupInput{FirewallRuleGroupID: "missing"})
	assert.True(t, cerrors.IsNotFound(err))

	a, err := m.AssociateFirewallRuleGroup(ctx, &driver.AssociateFirewallRuleGroupInput{
		FirewallRuleGroupID: rg.ID, Name: "assoc", Priority: 101, VPCID: "vpc-1",
	})
	require.NoError(t, err)
	assert.Equal(t, mutationProtectionDisabled, a.MutationProtection) // defaulted

	uAssoc, err := m.UpdateFirewallRuleGroupAssociation(ctx, &driver.UpdateFirewallRuleGroupAssociationInput{
		ID: a.ID, Name: ptr("assoc2"), Priority: ptr(int32(202)), MutationProtection: ptr("ENABLED"),
	})
	require.NoError(t, err)
	assert.Equal(t, "assoc2", uAssoc.Name)
	assert.Equal(t, int32(202), uAssoc.Priority)

	assocs, err := m.ListFirewallRuleGroupAssociations(ctx)
	require.NoError(t, err)
	assert.Len(t, assocs, 1)

	_, err = m.DisassociateFirewallRuleGroup(ctx, a.ID)
	require.NoError(t, err)
	_, err = m.GetFirewallRuleGroupAssociation(ctx, a.ID)
	assert.True(t, cerrors.IsNotFound(err))
	_, err = m.UpdateFirewallRuleGroupAssociation(ctx, &driver.UpdateFirewallRuleGroupAssociationInput{ID: "missing"})
	assert.True(t, cerrors.IsNotFound(err))

	// Firewall config lazy default + update, and rule-type enumeration is empty.
	fc, err := m.GetFirewallConfig(ctx, "vpc-1")
	require.NoError(t, err)
	assert.Equal(t, failOpenDisabled, fc.FirewallFailOpen)
	ufc, err := m.UpdateFirewallConfig(ctx, "vpc-1", failOpenEnabled)
	require.NoError(t, err)
	assert.Equal(t, failOpenEnabled, ufc.FirewallFailOpen)
	fcs, err := m.ListFirewallConfigs(ctx)
	require.NoError(t, err)
	assert.Len(t, fcs, 1)

	types, err := m.ListFirewallRuleTypes(ctx)
	require.NoError(t, err)
	assert.Empty(t, types)

	_, err = m.DeleteFirewallRuleGroup(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))
}

// ---- outpost resolvers ----

func TestOutpostResolverLifecycleAndErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	o, err := m.CreateOutpostResolver(ctx, &driver.CreateOutpostResolverInput{
		Name: "op", OutpostARN: "arn:aws:outposts:::op/1", PreferredInstanceType: "m5.large", InstanceCount: 4,
	})
	require.NoError(t, err)
	assert.Contains(t, o.ID, "rslvr-op-")

	// Zero-value fields on update leave existing values unchanged.
	upd, err := m.UpdateOutpostResolver(ctx, &driver.UpdateOutpostResolverInput{ID: o.ID, InstanceCount: ptr(int32(8))})
	require.NoError(t, err)
	assert.Equal(t, int32(8), upd.InstanceCount)
	assert.Equal(t, "op", upd.Name)
	assert.Equal(t, "m5.large", upd.PreferredInstanceType)

	list, err := m.ListOutpostResolvers(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	del, err := m.DeleteOutpostResolver(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, statusDeleting, del.Status)

	_, err = m.GetOutpostResolver(ctx, o.ID)
	assert.True(t, cerrors.IsNotFound(err))
	_, err = m.UpdateOutpostResolver(ctx, &driver.UpdateOutpostResolverInput{ID: "missing"})
	assert.True(t, cerrors.IsNotFound(err))
	_, err = m.DeleteOutpostResolver(ctx, "missing")
	assert.True(t, cerrors.IsNotFound(err))
}

// ---- tagging ----

func TestTaggingMergeAndUntag(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Tagging targets a real, live resource — a bogus ARN is rejected.
	ep, err := m.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
		Name: "ep", Direction: directionInbound,
		IPAddresses: []driver.IPAddress{{SubnetID: "s"}, {SubnetID: "s2"}},
	})
	require.NoError(t, err)

	arn := ep.ARN

	require.NoError(t, m.TagResource(ctx, arn, []driver.Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}))
	// Overlapping key overwrites by key.
	require.NoError(t, m.TagResource(ctx, arn, []driver.Tag{{Key: "a", Value: "9"}}))

	tags, err := m.ListTagsForResource(ctx, arn)
	require.NoError(t, err)
	got := map[string]string{}
	for _, tg := range tags {
		got[tg.Key] = tg.Value
	}
	assert.Equal(t, map[string]string{"a": "9", "b": "2"}, got)

	require.NoError(t, m.UntagResource(ctx, arn, []string{"a"}))
	tags, err = m.ListTagsForResource(ctx, arn)
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	assert.Equal(t, "b", tags[0].Key)

	// Tagging / listing an ARN that names no live resource is a NotFound.
	assert.True(t, cerrors.IsNotFound(m.TagResource(ctx, "arn:none", nil)))

	_, err = m.ListTagsForResource(ctx, "arn:none")
	assert.True(t, cerrors.IsNotFound(err))
}

// --- review-driven correctness behaviors ---

func TestDeleteBlockedByDependents(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Endpoint referenced by a rule cannot be deleted.
	ep, _ := m.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
		Direction: "OUTBOUND", IPAddresses: []driver.IPAddress{{SubnetID: "s"}, {SubnetID: "s2"}},
	})
	rule, _ := m.CreateResolverRule(ctx, &driver.CreateResolverRuleInput{
		Name: "r", RuleType: "FORWARD", DomainName: "x.com", ResolverEndpointID: ep.ID,
	})
	_, err := m.DeleteResolverEndpoint(ctx, ep.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	// Rule with a VPC association cannot be deleted.
	_, err = m.AssociateResolverRule(ctx, rule.ID, "vpc-1", "a")
	require.NoError(t, err)
	_, err = m.DeleteResolverRule(ctx, rule.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	// Domain list referenced by a firewall rule cannot be deleted.
	rg, _ := m.CreateFirewallRuleGroup(ctx, "", "rg", nil)
	dl, _ := m.CreateFirewallDomainList(ctx, "", "dl", nil)
	_, err = m.CreateFirewallRule(ctx, &driver.FirewallRuleInput{
		FirewallRuleGroupID: rg.ID, FirewallDomainListID: dl.ID, Priority: 1, Action: "BLOCK",
	})
	require.NoError(t, err)
	_, err = m.DeleteFirewallDomainList(ctx, dl.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	// Rule group with a VPC association cannot be deleted.
	_, err = m.AssociateFirewallRuleGroup(ctx, &driver.AssociateFirewallRuleGroupInput{
		FirewallRuleGroupID: rg.ID, VPCID: "vpc-1",
	})
	require.NoError(t, err)
	_, err = m.DeleteFirewallRuleGroup(ctx, rg.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err))
}

func TestFirewallRuleDuplicateAndAtomicBatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	rg, _ := m.CreateFirewallRuleGroup(ctx, "", "rg", nil)

	_, err := m.CreateFirewallRule(ctx, &driver.FirewallRuleInput{
		FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-1", Priority: 1, Action: "BLOCK",
	})
	require.NoError(t, err)

	// Duplicate (group, domain-list, qtype) is rejected.
	_, err = m.CreateFirewallRule(ctx, &driver.FirewallRuleInput{
		FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-1", Priority: 2, Action: "ALLOW",
	})
	assert.True(t, cerrors.IsAlreadyExists(err))

	// A batch containing an in-batch duplicate is rejected atomically — nothing
	// from the batch is stored, so RuleCount stays at the single prior rule.
	_, err = m.BatchCreateFirewallRules(ctx, []driver.FirewallRuleInput{
		{FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-2", Priority: 3, Action: "BLOCK"},
		{FirewallRuleGroupID: rg.ID, FirewallDomainListID: "dl-2", Priority: 4, Action: "BLOCK"},
	})
	assert.True(t, cerrors.IsAlreadyExists(err))

	rules, _ := m.ListFirewallRules(ctx, rg.ID)
	assert.Len(t, rules, 1)

	got, _ := m.GetFirewallRuleGroup(ctx, rg.ID)
	assert.Equal(t, int32(1), got.RuleCount)
}

func TestAssociationDedupe(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	rule, _ := m.CreateResolverRule(ctx, &driver.CreateResolverRuleInput{Name: "r", RuleType: "FORWARD", DomainName: "x"})
	_, err := m.AssociateResolverRule(ctx, rule.ID, "vpc-1", "a")
	require.NoError(t, err)
	_, err = m.AssociateResolverRule(ctx, rule.ID, "vpc-1", "a")
	assert.True(t, cerrors.IsAlreadyExists(err))

	qlc, _ := m.CreateResolverQueryLogConfig(ctx, &driver.CreateQueryLogConfigInput{Name: "q"})
	_, err = m.AssociateResolverQueryLogConfig(ctx, qlc.ID, "vpc-1")
	require.NoError(t, err)
	_, err = m.AssociateResolverQueryLogConfig(ctx, qlc.ID, "vpc-1")
	assert.True(t, cerrors.IsAlreadyExists(err))

	rg, _ := m.CreateFirewallRuleGroup(ctx, "", "rg", nil)
	_, err = m.AssociateFirewallRuleGroup(ctx, &driver.AssociateFirewallRuleGroupInput{FirewallRuleGroupID: rg.ID, VPCID: "vpc-1"})
	require.NoError(t, err)
	_, err = m.AssociateFirewallRuleGroup(ctx, &driver.AssociateFirewallRuleGroupInput{FirewallRuleGroupID: rg.ID, VPCID: "vpc-1"})
	assert.True(t, cerrors.IsAlreadyExists(err))
}

func TestCreatorRequestIDIdempotency(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	in := &driver.CreateResolverEndpointInput{
		CreatorRequestID: "tok-1", Name: "ep", Direction: directionInbound,
		IPAddresses: []driver.IPAddress{{SubnetID: "s"}, {SubnetID: "s2"}},
	}
	first, _ := m.CreateResolverEndpoint(ctx, in)
	second, _ := m.CreateResolverEndpoint(ctx, in)
	assert.Equal(t, first.ID, second.ID)

	list, _ := m.ListResolverEndpoints(ctx)
	assert.Len(t, list, 1)
}

func TestDisassociateIPMinimumTwo(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ep, _ := m.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
		Direction: directionInbound, IPAddresses: []driver.IPAddress{{SubnetID: "s1"}, {SubnetID: "s2"}},
	})

	// At exactly two IPs, a disassociate is rejected.
	_, err := m.DisassociateResolverEndpointIPAddress(ctx, ep.ID, &driver.IPAddress{SubnetID: "s1"})
	assert.True(t, cerrors.IsFailedPrecondition(err))
}

func TestPointerUpdateAppliesExplicitEmpty(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ep, _ := m.CreateResolverEndpoint(ctx, &driver.CreateResolverEndpointInput{
		Name: "named", Direction: directionInbound,
		IPAddresses: []driver.IPAddress{{SubnetID: "s"}, {SubnetID: "s2"}},
	})

	// nil pointer leaves the name unchanged...
	unchanged, _ := m.UpdateResolverEndpoint(ctx, ep.ID, driver.UpdateResolverEndpointInput{})
	assert.Equal(t, "named", unchanged.Name)

	// ...an explicit empty string clears it (distinct from "absent").
	cleared, _ := m.UpdateResolverEndpoint(ctx, ep.ID, driver.UpdateResolverEndpointInput{Name: ptr("")})
	assert.Equal(t, "", cleared.Name)
}

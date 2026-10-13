package vpclattice

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func ptr[T any](v T) *T { return &v }

func TestServiceNetworkCRUDAndIdentifierResolution(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sn, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	require.NoError(t, err)
	assert.Contains(t, sn.ID, "sn-")
	assert.Equal(t, authTypeNone, sn.AuthType)

	// Get by ARN (not just ID) resolves via idFromIdentifier.
	got, err := m.GetServiceNetwork(ctx, sn.ARN)
	require.NoError(t, err)
	assert.Equal(t, sn.ID, got.ID)

	_, err = m.GetServiceNetwork(ctx, "sn-missing")
	assert.True(t, cerrors.IsNotFound(err))
	assert.Error(t, m.DeleteServiceNetwork(ctx, "sn-missing"))
	_, err = m.UpdateServiceNetwork(ctx, "sn-missing", "AWS_IAM")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestServiceNetworkAssocCounts(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	rc, _ := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{Name: "rc", Type: "SINGLE"})

	_, err := m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-1"})
	require.NoError(t, err)
	_, err = m.CreateSNServiceAssociation(ctx, sn.ID, svc.ID, nil)
	require.NoError(t, err)
	_, err = m.CreateSNResourceAssociation(ctx, sn.ID, rc.ID, true, nil)
	require.NoError(t, err)

	got, err := m.GetServiceNetwork(ctx, sn.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.NumberOfAssociatedVPCs)
	assert.Equal(t, int64(1), got.NumberOfAssociatedServices)
	assert.Equal(t, int64(1), got.NumberOfAssociatedResourceConfigurations)

	// A resource association whose target no longer exists is not counted.
	require.NoError(t, m.DeleteResourceConfiguration(ctx, rc.ID))
	got, err = m.GetServiceNetwork(ctx, sn.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), got.NumberOfAssociatedResourceConfigurations)

	// Association against a missing network fails.
	_, err = m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: "sn-x", VpcID: "v"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestServiceDefaultsAndUpdate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	svc, err := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	require.NoError(t, err)
	assert.Equal(t, int32(defaultIdleTimeoutSec), svc.IdleTimeoutSeconds)
	assert.Equal(t, serviceStatusActive, svc.Status)
	assert.Contains(t, svc.DNSName, "vpc-lattice-svcs")

	upd, err := m.UpdateService(ctx, &driver.UpdateServiceInput{ID: svc.ID, IdleTimeoutSeconds: 120})
	require.NoError(t, err)
	assert.Equal(t, int32(120), upd.IdleTimeoutSeconds)

	_, err = m.UpdateService(ctx, &driver.UpdateServiceInput{ID: "svc-missing"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestListenerScopingAndCloneIsolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})

	// Create against a missing service fails.
	_, err := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: "svc-x"})
	assert.True(t, cerrors.IsNotFound(err))

	l, err := m.CreateListener(ctx, &driver.CreateListenerInput{
		ServiceID: svc.ID, Name: "http", Protocol: "HTTP", Port: 80,
		DefaultAction: []byte(`{"fixedResponse":{"statusCode":404}}`),
	})
	require.NoError(t, err)

	// A listener is not reachable via a different service ID.
	other, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc2"})
	_, err = m.GetListener(ctx, other.ID, l.ID)
	assert.True(t, cerrors.IsNotFound(err))

	// Mutating a returned copy's raw action must not corrupt the store.
	got, _ := m.GetListener(ctx, svc.ID, l.ID)
	got.DefaultAction[0] = 'X'
	reread, _ := m.GetListener(ctx, svc.ID, l.ID)
	assert.Equal(t, byte('{'), reread.DefaultAction[0])
}

func TestRuleBatchUpdatePartial(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	l, _ := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "HTTP", Port: 80})
	rule, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "r", Priority: 10})
	require.NoError(t, err)

	ok, fail, err := m.BatchUpdateRules(ctx, svc.ID, l.ID, []driver.RuleUpdate{
		{RuleID: rule.ID, Priority: 20},
		{RuleID: "rule-missing", Priority: 30},
	})
	require.NoError(t, err)
	assert.Len(t, ok, 1)
	assert.Len(t, fail, 1)
	assert.Equal(t, "ResourceNotFoundException", fail[0].FailureCode)

	// Create rule under a missing listener fails.
	_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: "listener-x"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestTargetGroupConfigExtractAndTargets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	tg, err := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{
		Name: "tg", Type: "IP",
		Config: []byte(`{"port":443,"protocol":"HTTPS","vpcIdentifier":"vpc-1"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(443), tg.Port)
	assert.Equal(t, "HTTPS", tg.Protocol)
	assert.Equal(t, "vpc-1", tg.VpcID)

	// Register dedups by id|port; deregister removes.
	_, _, err = m.RegisterTargets(ctx, tg.ID, []driver.RegisteredTarget{{ID: "10.0.0.1", Port: 443}, {ID: "10.0.0.1", Port: 443}})
	require.NoError(t, err)
	ts, _ := m.ListTargets(ctx, tg.ID)
	assert.Len(t, ts, 1)
	assert.Equal(t, targetStatusHealthy, ts[0].Status)

	_, _, err = m.DeregisterTargets(ctx, tg.ID, []driver.RegisteredTarget{{ID: "10.0.0.1", Port: 443}})
	require.NoError(t, err)
	ts, _ = m.ListTargets(ctx, tg.ID)
	assert.Empty(t, ts)

	_, _, err = m.RegisterTargets(ctx, "tg-missing", nil)
	assert.True(t, cerrors.IsNotFound(err))

	// UpdateTargetGroup merges healthCheck into the config blob.
	upd, err := m.UpdateTargetGroup(ctx, tg.ID, []byte(`{"enabled":true}`))
	require.NoError(t, err)
	assert.Contains(t, string(upd.Config), "healthCheck")
}

func TestPoliciesAndTagging(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	p, err := m.PutAuthPolicy(ctx, "sn-1", `{"v":1}`)
	require.NoError(t, err)
	assert.Equal(t, authPolicyStateActive, p.State)

	got, err := m.GetAuthPolicy(ctx, "sn-1")
	require.NoError(t, err)
	assert.Equal(t, `{"v":1}`, got.Policy)

	require.NoError(t, m.DeleteAuthPolicy(ctx, "sn-1"))
	_, err = m.GetAuthPolicy(ctx, "sn-1")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetResourcePolicy(ctx, "arn:none")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.PutResourcePolicy(ctx, "arn:x", "pol"))
	rp, err := m.GetResourcePolicy(ctx, "arn:x")
	require.NoError(t, err)
	assert.Equal(t, "pol", rp)

	const arn = "arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-1"
	require.NoError(t, m.TagResource(ctx, arn, map[string]string{"a": "1", "b": "2"}))
	require.NoError(t, m.TagResource(ctx, arn, map[string]string{"a": "9"}))
	require.NoError(t, m.UntagResource(ctx, arn, []string{"b"}))

	tags, err := m.ListTagsForResource(ctx, arn)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "9"}, tags)
}

func TestResourceEndpointAssociationEmpty(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	as, err := m.ListResourceEndpointAssociations(ctx)
	require.NoError(t, err)
	assert.Empty(t, as)

	assert.True(t, cerrors.IsNotFound(m.DeleteResourceEndpointAssociation(ctx, "rea-1")))
}

func TestResourceConfigGatewayCRUD(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	gw, err := m.CreateResourceGateway(ctx, &driver.CreateResourceGatewayInput{
		Name: "gw", VpcID: "vpc-1", SubnetIDs: []string{"s-1"}, SecurityGroupIDs: []string{"sg-1"}, IPAddressType: "IPV4",
	})
	require.NoError(t, err)
	assert.Contains(t, gw.ID, "rg-")

	ugw, err := m.UpdateResourceGateway(ctx, gw.ID, []string{"sg-1", "sg-2"})
	require.NoError(t, err)
	assert.Len(t, ugw.SecurityGroupIDs, 2)

	gotGw, err := m.GetResourceGateway(ctx, gw.ARN) // by ARN
	require.NoError(t, err)
	assert.Equal(t, gw.ID, gotGw.ID)

	gws, err := m.ListResourceGateways(ctx)
	require.NoError(t, err)
	assert.Len(t, gws, 1)

	_, err = m.GetResourceGateway(ctx, "rg-missing")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.DeleteResourceGateway(ctx, gw.ID))
	assert.True(t, cerrors.IsNotFound(m.DeleteResourceGateway(ctx, gw.ID)))

	rc, err := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{
		Name: "rc", Type: "SINGLE", Protocol: "TCP", PortRanges: []string{"443"},
		Definition: []byte(`{"ipResource":{"ipAddress":"10.0.0.9"}}`),
	})
	require.NoError(t, err)
	assert.Contains(t, rc.ID, "rc-")

	urc, err := m.UpdateResourceConfiguration(ctx, &driver.UpdateResourceConfigurationInput{ID: rc.ID, PortRanges: []string{"443", "8443"}})
	require.NoError(t, err)
	assert.Len(t, urc.PortRanges, 2)

	_, err = m.GetResourceConfiguration(ctx, rc.ID)
	require.NoError(t, err)
	rcs, err := m.ListResourceConfigurations(ctx)
	require.NoError(t, err)
	assert.Len(t, rcs, 1)

	_, err = m.GetResourceConfiguration(ctx, "rc-missing")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.DeleteResourceConfiguration(ctx, rc.ID))
	_, err = m.UpdateResourceConfiguration(ctx, &driver.UpdateResourceConfigurationInput{ID: "rc-missing"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestAccessLogAndDomainVerificationCRUD(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	asn, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "als-sn"})
	require.NoError(t, err)

	als, err := m.CreateAccessLogSubscription(ctx, asn.ARN, "arn:aws:s3:::bucket", "SERVICE", nil)
	require.NoError(t, err)
	assert.Contains(t, als.ID, "als-")
	assert.Equal(t, asn.ID, als.ResourceID)
	assert.NotEmpty(t, als.ResourceARN)

	uals, err := m.UpdateAccessLogSubscription(ctx, als.ID, "arn:aws:logs:::lg")
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:logs:::lg", uals.DestinationARN)

	_, err = m.GetAccessLogSubscription(ctx, als.ID)
	require.NoError(t, err)
	subs, err := m.ListAccessLogSubscriptions(ctx)
	require.NoError(t, err)
	assert.Len(t, subs, 1)

	_, err = m.GetAccessLogSubscription(ctx, "als-missing")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.DeleteAccessLogSubscription(ctx, als.ID))
	assert.True(t, cerrors.IsNotFound(m.DeleteAccessLogSubscription(ctx, als.ID)))

	dv, err := m.StartDomainVerification(ctx, "example.com", nil)
	require.NoError(t, err)
	assert.Equal(t, domainVerificationStatusPending, dv.Status)

	_, err = m.GetDomainVerification(ctx, dv.ID)
	require.NoError(t, err)
	dvs, err := m.ListDomainVerifications(ctx)
	require.NoError(t, err)
	assert.Len(t, dvs, 1)

	_, err = m.GetDomainVerification(ctx, "dv-missing")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.DeleteDomainVerification(ctx, dv.ID))
	assert.True(t, cerrors.IsNotFound(m.DeleteDomainVerification(ctx, "dv-missing")))
}

func TestListenerAndRuleListAndUpdate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	l, _ := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "HTTP", Port: 80})

	ul, err := m.UpdateListener(ctx, svc.ID, l.ID, []byte(`{"fixedResponse":{"statusCode":200}}`))
	require.NoError(t, err)
	assert.Contains(t, string(ul.DefaultAction), "200")

	ls, err := m.ListListeners(ctx, svc.ID)
	require.NoError(t, err)
	assert.Len(t, ls, 1)
	require.NoError(t, m.DeleteListener(ctx, svc.ID, l.ID))

	l2, _ := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "HTTP", Port: 81})
	r, _ := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l2.ID, Name: "r", Priority: 5})
	ur, err := m.UpdateRule(ctx, svc.ID, l2.ID, r.ID, 7, []byte(`{"httpMatch":{}}`), nil)
	require.NoError(t, err)
	assert.Equal(t, int32(7), ur.Priority)
	rs, err := m.ListRules(ctx, svc.ID, l2.ID)
	require.NoError(t, err)
	assert.Len(t, rs, 2) // the custom rule + the synthesized default rule
	require.NoError(t, m.DeleteRule(ctx, svc.ID, l2.ID, r.ID))
	_, err = m.GetRule(ctx, svc.ID, l2.ID, r.ID)
	assert.True(t, cerrors.IsNotFound(err))

	// SN-VPC association update + endpoint-assoc empty list.
	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	a, _ := m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-1"})
	ua, err := m.UpdateSNVpcAssociation(ctx, a.ID, []string{"sg-9"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sg-9"}, ua.SecurityGroupIDs)
	ep, err := m.ListSNVpcEndpointAssociations(ctx, sn.ID)
	require.NoError(t, err)
	assert.Empty(t, ep)
}

// --- review-driven correctness behaviors ---

func TestCreateWritesTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sn, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{
		Name: "sn", Tags: map[string]string{"team": "net", "env": "test"},
	})
	require.NoError(t, err)

	tags, err := m.ListTagsForResource(ctx, sn.ARN)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"team": "net", "env": "test"}, tags)
}

func TestServiceNetworkDeleteBlockedByAssociation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	_, err := m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-1"})
	require.NoError(t, err)

	err = m.DeleteServiceNetwork(ctx, sn.ID)
	assert.True(t, cerrors.IsFailedPrecondition(err))
}

func TestServiceDeleteGuardAndCascade(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})

	// Associated service can't be deleted.
	_, err := m.CreateSNServiceAssociation(ctx, sn.ID, svc.ID, nil)
	require.NoError(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(m.DeleteService(ctx, svc.ID)))

	// A standalone service with a listener + rule cascades on delete.
	svc2, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc2"})
	l, _ := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc2.ID, Protocol: "HTTP", Port: 80})
	r, _ := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc2.ID, ListenerID: l.ID, Name: "r", Priority: 1})

	require.NoError(t, m.DeleteService(ctx, svc2.ID))
	_, err = m.GetListener(ctx, svc2.ID, l.ID)
	assert.True(t, cerrors.IsNotFound(err))
	_, err = m.GetRule(ctx, svc2.ID, l.ID, r.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestUpdateResourceConfigKeepsAllowFlag(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	rc, _ := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{
		Name: "rc", Type: "SINGLE", AllowAssociationToShared: true,
	})

	// A partial update that omits the flag (nil) must not reset it to false.
	upd, err := m.UpdateResourceConfiguration(ctx, &driver.UpdateResourceConfigurationInput{
		ID: rc.ID, PortRanges: []string{"443"},
	})
	require.NoError(t, err)
	assert.True(t, upd.AllowAssociationToShared)

	// An explicit false clears it.
	upd, err = m.UpdateResourceConfiguration(ctx, &driver.UpdateResourceConfigurationInput{
		ID: rc.ID, AllowAssociationToShared: ptr(false),
	})
	require.NoError(t, err)
	assert.False(t, upd.AllowAssociationToShared)
}

// --- #1451 real-cloud parity ---

func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAsyncSettle())

	return New(opts), fc
}

func forwardTo(tgIdentifier string) []byte {
	return []byte(`{"forward":{"targetGroups":[{"targetGroupIdentifier":"` + tgIdentifier + `","weight":1}]}}`)
}

func svcAndListener(t *testing.T, m *Mock, name string) (*driver.Service, *driver.Listener) {
	t.Helper()

	svc, err := m.CreateService(context.Background(), &driver.CreateServiceInput{Name: name})
	require.NoError(t, err)

	l, err := m.CreateListener(context.Background(), &driver.CreateListenerInput{
		ServiceID: svc.ID, Protocol: "HTTP", Port: 80, DefaultAction: []byte(`{"fixedResponse":{"statusCode":404}}`),
	})
	require.NoError(t, err)

	return svc, l
}

type fakeVPCs map[string]bool

func (f fakeVPCs) DescribeVPCs(_ context.Context, ids []string) ([]netdriver.VPCInfo, error) {
	for _, id := range ids {
		if !f[id] {
			return nil, cerrors.Newf(cerrors.NotFound, "vpc %q not found", id)
		}
	}

	return nil, nil
}

func TestCreateTargetGroup_InvalidType(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "bad", Type: "BOGUS"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "empty"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	for _, typ := range []string{"IP", "LAMBDA", "INSTANCE", "ALB"} {
		_, err = m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg-" + typ, Type: typ})
		require.NoError(t, err, typ)
	}

	tgs, _ := m.ListTargetGroups(ctx)
	assert.Len(t, tgs, 4)
}

func TestDeleteTargetGroup_InUse(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})

	rule, err := m.CreateRule(ctx, &driver.CreateRuleInput{
		ServiceID: svc.ID, ListenerID: l.ID, Name: "r", Priority: 1, Action: forwardTo(tg.ARN),
	})
	require.NoError(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(m.DeleteTargetGroup(ctx, tg.ID)))

	// Id form of the identifier is matched too.
	_, err = m.UpdateRule(ctx, svc.ID, l.ID, rule.ID, 0, nil, forwardTo(tg.ID))
	require.NoError(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(m.DeleteTargetGroup(ctx, tg.ARN)))

	require.NoError(t, m.DeleteRule(ctx, svc.ID, l.ID, rule.ID))
	require.NoError(t, m.DeleteTargetGroup(ctx, tg.ID))
}

func TestDeleteTargetGroup_ListenerDefaultOnly(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})

	_, err := m.UpdateListener(ctx, svc.ID, l.ID, forwardTo(tg.ID))
	require.NoError(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(m.DeleteTargetGroup(ctx, tg.ID)))

	_, err = m.UpdateListener(ctx, svc.ID, l.ID, []byte(`{"fixedResponse":{"statusCode":200}}`))
	require.NoError(t, err)
	require.NoError(t, m.DeleteTargetGroup(ctx, tg.ID))
}

func TestTargetGroupRefs_ActionForms(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})

	for _, bad := range []string{`not json`, `{"forward":`, `{"forward":{"targetGroups":"x"}}`, `[]`, ``} {
		_, err := m.UpdateListener(ctx, svc.ID, l.ID, []byte(bad))
		require.NoError(t, err)
		got, err := m.GetTargetGroup(ctx, tg.ID)
		require.NoError(t, err)
		assert.Empty(t, got.ServiceARNs, bad)
	}

	require.NoError(t, m.DeleteTargetGroup(ctx, tg.ID))
}

func TestCreateListener_Validation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})

	for _, c := range []struct {
		proto string
		port  int32
	}{{"FTP", 80}, {"", 80}, {"HTTP", -1}, {"HTTP", 65536}, {"HTTP", 999999}} {
		_, err := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: c.proto, Port: c.port})
		assert.True(t, cerrors.IsInvalidArgument(err), "%s/%d", c.proto, c.port)
	}

	l, err := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "HTTP"})
	require.NoError(t, err)
	assert.Equal(t, int32(80), l.Port)

	l, err = m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "HTTPS"})
	require.NoError(t, err)
	assert.Equal(t, int32(443), l.Port)

	_, err = m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "TLS_PASSTHROUGH", Port: 8443})
	require.NoError(t, err)

	// TLS_PASSTHROUGH has no documented default port, and a listener's port is 1-65535.
	_, err = m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "TLS_PASSTHROUGH"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Failed creates stored nothing (B5).
	ls, _ := m.ListListeners(ctx, svc.ID)
	assert.Len(t, ls, 3)
}

func TestAuthTypeValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "bad", AuthType: "BOGUS"})
	assert.True(t, cerrors.IsInvalidArgument(err))
	_, err = m.CreateService(ctx, &driver.CreateServiceInput{Name: "bad", AuthType: "BOGUS"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	sn, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn", AuthType: "AWS_IAM"})
	require.NoError(t, err)
	svc, err := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	require.NoError(t, err)
	assert.Equal(t, authTypeNone, svc.AuthType)

	_, err = m.UpdateServiceNetwork(ctx, sn.ID, "BOGUS")
	assert.True(t, cerrors.IsInvalidArgument(err))
	_, err = m.UpdateService(ctx, &driver.UpdateServiceInput{ID: svc.ID, AuthType: "BOGUS"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	got, _ := m.GetServiceNetwork(ctx, sn.ID)
	assert.Equal(t, "AWS_IAM", got.AuthType)
}

func TestCreateService_DuplicateName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	svc, err := m.CreateService(ctx, &driver.CreateServiceInput{Name: "dup"})
	require.NoError(t, err)

	_, err = m.CreateService(ctx, &driver.CreateServiceInput{Name: "dup"})
	assert.True(t, cerrors.IsAlreadyExists(err))

	require.NoError(t, m.DeleteService(ctx, svc.ID))
	_, err = m.CreateService(ctx, &driver.CreateServiceInput{Name: "dup"})
	require.NoError(t, err)
}

func TestCreateServiceNetwork_DuplicateName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sn, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "dup"})
	require.NoError(t, err)

	_, err = m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "dup"})
	assert.True(t, cerrors.IsAlreadyExists(err))

	require.NoError(t, m.DeleteServiceNetwork(ctx, sn.ID))
	_, err = m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "dup"})
	require.NoError(t, err)
}

func TestRulePriority_Conflict(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	l2, _ := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "HTTP", Port: 81})

	r1, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "a", Priority: 10})
	require.NoError(t, err)

	_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "b", Priority: 10})
	assert.True(t, cerrors.IsAlreadyExists(err))

	// Same priority on another listener is fine.
	_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l2.ID, Name: "c", Priority: 10})
	require.NoError(t, err)

	r2, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "d", Priority: 11})
	require.NoError(t, err)

	_, err = m.UpdateRule(ctx, svc.ID, l.ID, r2.ID, 10, nil, nil)
	assert.True(t, cerrors.IsAlreadyExists(err))

	for _, p := range []int32{0, -1, 2001} {
		_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "x", Priority: p})
		assert.True(t, cerrors.IsInvalidArgument(err), "%d", p)
	}

	// The documented range is 1-2000 (CreateRule / UpdateRule API reference).
	for _, p := range []int32{500, 1000, 2000} {
		_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "hi", Priority: p})
		require.NoError(t, err, "%d", p)
	}

	_, err = m.UpdateRule(ctx, svc.ID, l.ID, r2.ID, 2001, nil, nil)
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Freed priority is reusable.
	require.NoError(t, m.DeleteRule(ctx, svc.ID, l.ID, r1.ID))
	_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "e", Priority: 10})
	require.NoError(t, err)
}

func TestUpdateRule_OwnPriorityAllowed(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	r, _ := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "a", Priority: 10})

	_, err := m.UpdateRule(ctx, svc.ID, l.ID, r.ID, 10, []byte(`{"httpMatch":{}}`), nil)
	require.NoError(t, err)
}

func TestBatchUpdateRules_SamePriorityTwice(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	a, _ := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "a", Priority: 1})
	b, _ := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "b", Priority: 2})

	ok, fail, err := m.BatchUpdateRules(ctx, svc.ID, l.ID, []driver.RuleUpdate{
		{RuleID: a.ID, Priority: 50}, {RuleID: b.ID, Priority: 50},
	})
	require.NoError(t, err)
	require.Len(t, ok, 1)
	require.Len(t, fail, 1)
	assert.Equal(t, b.ID, fail[0].RuleID)
	assert.Equal(t, "ConflictException", fail[0].FailureCode)
}

func TestBatchUpdateRules_FailureShape(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	a, _ := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "a", Priority: 1})

	_, fail, err := m.BatchUpdateRules(ctx, svc.ID, l.ID, []driver.RuleUpdate{
		{RuleID: "rule-missing", Priority: 5}, {RuleID: a.ID, Priority: 2001},
	})
	require.NoError(t, err)
	require.Len(t, fail, 2)
	assert.Equal(t, "ResourceNotFoundException", fail[0].FailureCode)
	assert.NotContains(t, fail[0].FailureMessage, "NotFound:")
	assert.Contains(t, fail[0].FailureMessage, "rule-missing")
	assert.Equal(t, "ValidationException", fail[1].FailureCode)
}

func TestSNServiceAssociation_Duplicate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	sn2, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn2"})
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})

	a, err := m.CreateSNServiceAssociation(ctx, sn.ID, svc.ID, nil)
	require.NoError(t, err)

	_, err = m.CreateSNServiceAssociation(ctx, sn.ARN, svc.ARN, nil)
	assert.True(t, cerrors.IsAlreadyExists(err))

	_, err = m.CreateSNServiceAssociation(ctx, sn2.ID, svc.ID, nil)
	require.NoError(t, err)

	require.NoError(t, m.DeleteSNServiceAssociation(ctx, a.ID))
	_, err = m.CreateSNServiceAssociation(ctx, sn.ID, svc.ID, nil)
	require.NoError(t, err)
}

func TestSNVpcAssociation_UnknownVPC(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	m.SetNetworking(fakeVPCs{"vpc-ok": true})
	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})

	_, err := m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-nope"})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-ok"})
	require.NoError(t, err)

	// Missing network is still reported first.
	_, err = m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: "sn-x", VpcID: "vpc-ok"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestSNVpcAssociation_LookupNotWired(t *testing.T) {
	m := newTestMock()
	sn, _ := m.CreateServiceNetwork(context.Background(), &driver.CreateServiceNetworkInput{Name: "sn"})

	_, err := m.CreateSNVpcAssociation(context.Background(),
		&driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-anything"})
	require.NoError(t, err)
}

func TestAccessLogSubscription_UnknownResource(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateAccessLogSubscription(ctx, "sn-missing", "arn:aws:s3:::b", "", nil)
	assert.True(t, cerrors.IsNotFound(err))

	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	rc, _ := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{Name: "rc", Type: "SINGLE"})

	for _, id := range []string{sn.ID, sn.ARN, svc.ID, svc.ARN, rc.ID, rc.ARN} {
		_, err = m.CreateAccessLogSubscription(ctx, id, "arn:aws:s3:::b", "", nil)
		require.NoError(t, err, id)
	}

	subs, _ := m.ListAccessLogSubscriptions(ctx)
	assert.Len(t, subs, 6)
}

func TestDeleteResourceGateway_InUse(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	gw, _ := m.CreateResourceGateway(ctx, &driver.CreateResourceGatewayInput{Name: "gw", VpcID: "vpc-1"})
	rc, _ := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{
		Name: "rc", Type: "SINGLE", ResourceGatewayID: gw.ARN,
	})

	assert.True(t, cerrors.IsFailedPrecondition(m.DeleteResourceGateway(ctx, gw.ID)))
	require.NoError(t, m.DeleteResourceConfiguration(ctx, rc.ID))
	require.NoError(t, m.DeleteResourceGateway(ctx, gw.ID))
}

func TestTargetGroup_ServiceArns(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	s1, l1 := svcAndListener(t, m, "bsvc")
	s2, l2 := svcAndListener(t, m, "asvc")
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})

	got, _ := m.GetTargetGroup(ctx, tg.ID)
	assert.Empty(t, got.ServiceARNs)

	r1, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: s1.ID, ListenerID: l1.ID, Name: "r", Priority: 1, Action: forwardTo(tg.ID)})
	require.NoError(t, err)
	_, err = m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: s1.ID, ListenerID: l1.ID, Name: "r2", Priority: 2, Action: forwardTo(tg.ARN)})
	require.NoError(t, err)
	_, err = m.UpdateListener(ctx, s2.ID, l2.ID, forwardTo(tg.ID))
	require.NoError(t, err)

	want := []string{s2.ARN, s1.ARN} // sorted by ARN: asvc/bsvc ids differ, so sort explicitly
	sort.Strings(want)

	got, _ = m.GetTargetGroup(ctx, tg.ID)
	assert.Equal(t, want, got.ServiceARNs)

	list, _ := m.ListTargetGroups(ctx)
	require.Len(t, list, 1)
	assert.Equal(t, want, list[0].ServiceARNs)

	// Mutating the returned copy does not touch state (B3).
	got.ServiceARNs[0] = "mutated"
	again, _ := m.GetTargetGroup(ctx, tg.ID)
	assert.Equal(t, want, again.ServiceARNs)

	require.NoError(t, m.DeleteRule(ctx, s1.ID, l1.ID, r1.ID))
	got, _ = m.GetTargetGroup(ctx, tg.ID)
	assert.Len(t, got.ServiceARNs, 2) // second s1 rule + s2 listener still reference it
}

func TestTargetGroup_ServiceArns_AfterServiceDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	s, l := svcAndListener(t, m, "svc")
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})
	_, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: s.ID, ListenerID: l.ID, Name: "r", Priority: 1, Action: forwardTo(tg.ID)})
	require.NoError(t, err)

	require.NoError(t, m.DeleteService(ctx, s.ID))
	got, _ := m.GetTargetGroup(ctx, tg.ID)
	assert.Empty(t, got.ServiceARNs)
	require.NoError(t, m.DeleteTargetGroup(ctx, tg.ID))
}

func TestListRules_DefaultRule(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	_, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "a", Priority: 5})
	require.NoError(t, err)

	rs, err := m.ListRules(ctx, svc.ID, l.ID)
	require.NoError(t, err)
	require.Len(t, rs, 2)
	assert.True(t, rs[0].IsDefault)
	assert.Equal(t, "default", rs[0].Name)
	assert.JSONEq(t, string(l.DefaultAction), string(rs[0].Action))
	assert.False(t, rs[1].IsDefault)

	got, err := m.GetRule(ctx, svc.ID, l.ID, rs[0].ID)
	require.NoError(t, err)
	assert.True(t, got.IsDefault)

	assert.True(t, cerrors.IsInvalidArgument(m.DeleteRule(ctx, svc.ID, l.ID, rs[0].ID)))

	// AWS: "You can't modify a default listener rule. To modify a default
	// listener rule, use UpdateListener."
	_, err = m.UpdateRule(ctx, svc.ID, l.ID, rs[0].ID, 0, nil, []byte(`{"fixedResponse":{"statusCode":503}}`))
	assert.True(t, cerrors.IsInvalidArgument(err))
	_, err = m.UpdateRule(ctx, svc.ID, l.ID, rs[0].ID, 7, nil, nil)
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, fail, err := m.BatchUpdateRules(ctx, svc.ID, l.ID, []driver.RuleUpdate{
		{RuleID: rs[0].ID, Action: []byte(`{"fixedResponse":{"statusCode":503}}`)},
	})
	require.NoError(t, err)
	require.Len(t, fail, 1)
	assert.Equal(t, rs[0].ID, fail[0].RuleID)
	assert.Equal(t, "ValidationException", fail[0].FailureCode)

	// The listener's default action is untouched; UpdateListener is the way to change it.
	lg, _ := m.GetListener(ctx, svc.ID, l.ID)
	assert.JSONEq(t, string(l.DefaultAction), string(lg.DefaultAction))

	_, err = m.UpdateListener(ctx, svc.ID, l.ID, []byte(`{"fixedResponse":{"statusCode":503}}`))
	require.NoError(t, err)
	got, err = m.GetRule(ctx, svc.ID, l.ID, rs[0].ID)
	require.NoError(t, err)
	assert.Contains(t, string(got.Action), "503")

	require.NoError(t, m.DeleteListener(ctx, svc.ID, l.ID))
}

func TestRegisterTargets_Settle(t *testing.T) {
	ctx := context.Background()
	m, fc := newAsyncMock()
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})

	_, _, err := m.RegisterTargets(ctx, tg.ID, []driver.RegisteredTarget{{ID: "10.0.0.1", Port: 80}})
	require.NoError(t, err)

	ts, _ := m.ListTargets(ctx, tg.ID)
	require.Len(t, ts, 1)
	assert.Equal(t, "INITIAL", ts[0].Status)

	fc.Advance(settle.DefaultTargetHealthSettle)
	ts, _ = m.ListTargets(ctx, tg.ID)
	assert.Equal(t, "HEALTHY", ts[0].Status)

	// Sync mode keeps the immediate HEALTHY behaviour.
	sm := newTestMock()
	stg, _ := sm.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})
	_, _, _ = sm.RegisterTargets(ctx, stg.ID, []driver.RegisteredTarget{{ID: "10.0.0.1", Port: 80}})
	ts, _ = sm.ListTargets(ctx, stg.ID)
	assert.Equal(t, "HEALTHY", ts[0].Status)
}

func TestPutAuthPolicy_InvalidJSON(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, bad := range []string{`{bad`, ``, `[]`, `"str"`, `123`, `null`} {
		_, err := m.PutAuthPolicy(ctx, "sn-1", bad)
		assert.True(t, cerrors.IsInvalidArgument(err), bad)
	}

	_, err := m.GetAuthPolicy(ctx, "sn-1")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.PutAuthPolicy(ctx, "sn-1", `{"Version":"2012-10-17"}`)
	require.NoError(t, err)
}

func TestStatusSettle(t *testing.T) {
	ctx := context.Background()
	m, fc := newAsyncMock()

	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn"})
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "tg", Type: "IP"})
	gw, _ := m.CreateResourceGateway(ctx, &driver.CreateResourceGatewayInput{Name: "gw", VpcID: "vpc-1"})
	rc, _ := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{Name: "rc", Type: "SINGLE"})
	va, _ := m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-1"})
	sa, _ := m.CreateSNServiceAssociation(ctx, sn.ID, svc.ID, nil)
	ra, _ := m.CreateSNResourceAssociation(ctx, sn.ID, rc.ID, false, nil)

	statuses := func() []string {
		s, _ := m.GetService(ctx, svc.ID)
		g, _ := m.GetTargetGroup(ctx, tg.ID)
		rg, _ := m.GetResourceGateway(ctx, gw.ID)
		c, _ := m.GetResourceConfiguration(ctx, rc.ID)
		v, _ := m.GetSNVpcAssociation(ctx, va.ID)
		sv, _ := m.GetSNServiceAssociation(ctx, sa.ID)
		r, _ := m.GetSNResourceAssociation(ctx, ra.ID)

		return []string{s.Status, g.Status, rg.Status, c.Status, v.Status, sv.Status, r.Status}
	}

	for _, s := range statuses() {
		assert.Equal(t, "CREATE_IN_PROGRESS", s)
	}

	// List paths observe the same overlay.
	svcs, _ := m.ListServices(ctx)
	assert.Equal(t, "CREATE_IN_PROGRESS", svcs[0].Status)

	fc.Advance(settle.DefaultLatticeSettle)

	for _, s := range statuses() {
		assert.Equal(t, "ACTIVE", s)
	}

	// Sync mode is ACTIVE immediately (existing behaviour).
	sm := newTestMock()
	ss, _ := sm.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})
	assert.Equal(t, "ACTIVE", ss.Status)
}

func TestSnapshotRestore_FinalState(t *testing.T) {
	ctx := context.Background()
	src, _ := newAsyncMock()
	svc, _ := src.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))
	got, err := dst.GetService(ctx, svc.ID)
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", got.Status)
}

func TestConcurrentCreates_Race(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	svc, l := svcAndListener(t, m, "svc")
	sn, _ := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "sn0"})
	svcB, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svcB"})

	const n = 50

	run := func(fn func(i int) error) (wins int) {
		var (
			wg sync.WaitGroup
			mu sync.Mutex
		)

		for i := 0; i < n; i++ {
			wg.Add(1)

			go func(i int) {
				defer wg.Done()

				if fn(i) == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}(i)
		}

		wg.Wait()

		return wins
	}

	assert.Equal(t, 1, run(func(int) error {
		_, err := m.CreateService(ctx, &driver.CreateServiceInput{Name: "same"})
		return err
	}))
	assert.Equal(t, 1, run(func(int) error {
		_, err := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "same"})
		return err
	}))
	assert.Equal(t, 1, run(func(int) error {
		_, err := m.CreateRule(ctx, &driver.CreateRuleInput{ServiceID: svc.ID, ListenerID: l.ID, Name: "r", Priority: 42})
		return err
	}))
	assert.Equal(t, 1, run(func(int) error {
		_, err := m.CreateSNServiceAssociation(ctx, sn.ID, svcB.ID, nil)
		return err
	}))
}

func TestValidationInProvider(t *testing.T) {
	// B1: the library (no HTTP layer) rejects every validation item.
	m := newTestMock()
	ctx := context.Background()
	svc, _ := m.CreateService(ctx, &driver.CreateServiceInput{Name: "svc"})

	_, e1 := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "t", Type: "X"})
	_, e2 := m.CreateListener(ctx, &driver.CreateListenerInput{ServiceID: svc.ID, Protocol: "FTP", Port: 1})
	_, e3 := m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "s", AuthType: "X"})
	_, e4 := m.PutAuthPolicy(ctx, "sn-1", "{")

	for _, err := range []error{e1, e2, e3, e4} {
		assert.True(t, cerrors.IsInvalidArgument(err))
	}
}

func TestReadsAreDeepCopies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	tg, _ := m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{
		Name: "tg", Type: "IP", Config: []byte(`{"port":80}`),
	})
	_, _, _ = m.RegisterTargets(ctx, tg.ID, []driver.RegisteredTarget{{ID: "10.0.0.1", Port: 80}})

	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			g, _ := m.GetTargetGroup(ctx, tg.ID)
			g.Config[0] = 'X'
			g.ServiceARNs = append(g.ServiceARNs, "x")
		}()

		go func() {
			defer wg.Done()

			_, _ = m.ListTargetGroups(ctx)
			_, _ = m.ListTargets(ctx, tg.ID)
		}()
	}

	wg.Wait()

	got, _ := m.GetTargetGroup(ctx, tg.ID)
	assert.Equal(t, byte('{'), got.Config[0])
}

func TestFailedCreatesMutateNothing(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.CreateTargetGroup(ctx, &driver.CreateTargetGroupInput{Name: "t", Type: "X"})
	_, _ = m.CreateServiceNetwork(ctx, &driver.CreateServiceNetworkInput{Name: "s", AuthType: "X"})
	_, _ = m.CreateAccessLogSubscription(ctx, "sn-none", "arn:aws:s3:::b", "", map[string]string{"k": "v"})

	tgs, _ := m.ListTargetGroups(ctx)
	sns, _ := m.ListServiceNetworks(ctx)
	subs, _ := m.ListAccessLogSubscriptions(ctx)
	assert.Empty(t, tgs)
	assert.Empty(t, sns)
	assert.Empty(t, subs)
	assert.Empty(t, m.tags.All())
}

func TestListListeners_UnknownService(t *testing.T) {
	m := newTestMock()

	_, err := m.ListListeners(context.Background(), "svc-missing")
	assert.True(t, cerrors.IsNotFound(err))
}

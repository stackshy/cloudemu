package vpclattice

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

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

	_, err := m.CreateSNVpcAssociation(ctx, &driver.CreateSNVpcAssociationInput{ServiceNetworkID: sn.ID, VpcID: "vpc-1"})
	require.NoError(t, err)
	_, err = m.CreateSNServiceAssociation(ctx, sn.ID, svc.ID, nil)
	require.NoError(t, err)
	_, err = m.CreateSNResourceAssociation(ctx, sn.ID, "rcfg-1", true, nil)
	require.NoError(t, err)

	got, err := m.GetServiceNetwork(ctx, sn.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.NumberOfAssociatedVPCs)
	assert.Equal(t, int64(1), got.NumberOfAssociatedServices)
	assert.Equal(t, int64(1), got.NumberOfAssociatedResourceConfigurations)

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
	assert.Contains(t, gw.ID, "rgw-")

	ugw, err := m.UpdateResourceGateway(ctx, gw.ID, []string{"sg-1", "sg-2"})
	require.NoError(t, err)
	assert.Len(t, ugw.SecurityGroupIDs, 2)

	gotGw, err := m.GetResourceGateway(ctx, gw.ARN) // by ARN
	require.NoError(t, err)
	assert.Equal(t, gw.ID, gotGw.ID)

	gws, err := m.ListResourceGateways(ctx)
	require.NoError(t, err)
	assert.Len(t, gws, 1)

	_, err = m.GetResourceGateway(ctx, "rgw-missing")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.DeleteResourceGateway(ctx, gw.ID))
	assert.True(t, cerrors.IsNotFound(m.DeleteResourceGateway(ctx, gw.ID)))

	rc, err := m.CreateResourceConfiguration(ctx, &driver.CreateResourceConfigurationInput{
		Name: "rc", Type: "SINGLE", Protocol: "TCP", PortRanges: []string{"443"},
		Definition: []byte(`{"ipResource":{"ipAddress":"10.0.0.9"}}`),
	})
	require.NoError(t, err)
	assert.Contains(t, rc.ID, "rcfg-")

	urc, err := m.UpdateResourceConfiguration(ctx, &driver.UpdateResourceConfigurationInput{ID: rc.ID, PortRanges: []string{"443", "8443"}})
	require.NoError(t, err)
	assert.Len(t, urc.PortRanges, 2)

	_, err = m.GetResourceConfiguration(ctx, rc.ID)
	require.NoError(t, err)
	rcs, err := m.ListResourceConfigurations(ctx)
	require.NoError(t, err)
	assert.Len(t, rcs, 1)

	_, err = m.GetResourceConfiguration(ctx, "rcfg-missing")
	assert.True(t, cerrors.IsNotFound(err))
	require.NoError(t, m.DeleteResourceConfiguration(ctx, rc.ID))
	_, err = m.UpdateResourceConfiguration(ctx, &driver.UpdateResourceConfigurationInput{ID: "rcfg-missing"})
	assert.True(t, cerrors.IsNotFound(err))
}

func TestAccessLogAndDomainVerificationCRUD(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	als, err := m.CreateAccessLogSubscription(ctx,
		"arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-1", "arn:aws:s3:::bucket", "SERVICE", nil)
	require.NoError(t, err)
	assert.Contains(t, als.ID, "als-")
	assert.Equal(t, "sn-1", als.ResourceID)
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
	assert.Len(t, rs, 1)
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

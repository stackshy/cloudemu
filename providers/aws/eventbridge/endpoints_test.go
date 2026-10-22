package eventbridge

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testHealthCheck = "arn:aws:route53:::healthcheck/abc-123"
	testRole        = "arn:aws:iam::123456789012:role/replication"
)

func endpointBusARN(region, name string) string {
	return "arn:aws:events:" + region + ":123456789012:event-bus/" + name
}

func endpointConfig(name string) *driver.EndpointConfig {
	return &driver.EndpointConfig{
		Name:             name,
		Routing:          driver.EndpointRouting{HealthCheck: testHealthCheck, SecondaryRoute: "us-west-2"},
		ReplicationState: replicationDisabled,
		EventBusARNs:     []string{endpointBusARN("us-east-1", "b1"), endpointBusARN("us-west-2", "b1")},
	}
}

func TestEndpointLifecycle(t *testing.T) {
	ctx := context.Background()
	m, fc := newTestMock()

	created, err := m.CreateEndpoint(ctx, endpointConfig("ep1"))
	require.NoError(t, err)
	assert.Equal(t, "CREATING", created.State)
	assert.Equal(t, "arn:aws:events:us-east-1:"+m.opts.AccountID+":endpoint/ep1", created.ARN)
	assert.Regexp(t, regexp.MustCompile(`^[a-z0-9]{10}\.[a-z0-9]{3}$`), created.EndpointID)
	assert.Equal(t, "https://"+created.EndpointID+".endpoint.events.amazonaws.com", created.EndpointURL)
	assert.Equal(t, "us-east-1", created.HomeRegion)

	got, err := m.DescribeEndpoint(ctx, "ep1", "")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", got.State, "with async settling off a create reads as ACTIVE at once")
	assert.Equal(t, created.EndpointID, got.EndpointID)

	_, err = m.DescribeEndpoint(ctx, "ep1", "eu-west-1")
	assert.True(t, errors.IsNotFound(err), "another home Region does not find it")

	fc.Advance(time.Minute)

	desc := "updated"
	upd, err := m.UpdateEndpoint(ctx, &driver.EndpointUpdate{
		Name: "ep1", Description: &desc,
		ReplicationState: strPtr(replicationEnabled), RoleARN: strPtr(testRole),
	})
	require.NoError(t, err)
	assert.Equal(t, "UPDATING", upd.State)
	assert.Equal(t, replicationEnabled, upd.ReplicationState)
	assert.True(t, upd.LastModifiedAt.After(upd.CreatedAt))

	got, err = m.DescribeEndpoint(ctx, "ep1", "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", got.State)
	assert.Equal(t, "updated", got.Description)
	assert.Equal(t, testHealthCheck, got.Routing.HealthCheck, "omitted fields keep their values")

	require.NoError(t, m.DeleteEndpoint(ctx, "ep1"))

	_, err = m.DescribeEndpoint(ctx, "ep1", "")
	assert.True(t, errors.IsNotFound(err))
	assert.True(t, errors.IsNotFound(m.DeleteEndpoint(ctx, "ep1")))

	_, err = m.UpdateEndpoint(ctx, &driver.EndpointUpdate{Name: "ep1", Description: &desc})
	assert.True(t, errors.IsNotFound(err))
}

func strPtr(s string) *string { return &s }

func TestEndpointAsyncSettle(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAsyncSettle()))

	_, err := m.CreateEndpoint(ctx, endpointConfig("ep"))
	require.NoError(t, err)

	got, err := m.DescribeEndpoint(ctx, "ep", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATING", got.State)

	fc.Advance(settle.DefaultEventBridgeEndpointSettle)

	got, err = m.DescribeEndpoint(ctx, "ep", "")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", got.State)

	_, err = m.UpdateEndpoint(ctx, &driver.EndpointUpdate{Name: "ep", Description: strPtr("x")})
	require.NoError(t, err)

	list, _, err := m.ListEndpoints(ctx, driver.EndpointFilter{})
	require.NoError(t, err)
	assert.Equal(t, "UPDATING", list[0].State)

	fc.Advance(settle.DefaultEventBridgeEndpointSettle)

	got, err = m.DescribeEndpoint(ctx, "ep", "")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", got.State)
}

func TestEndpointCreateDefaultsAndDuplicates(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	cfg := endpointConfig("ep")
	cfg.ReplicationState = ""
	cfg.RoleARN = testRole

	ep, err := m.CreateEndpoint(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, replicationEnabled, ep.ReplicationState, "replication defaults to ENABLED")

	_, err = m.CreateEndpoint(ctx, cfg)
	assert.True(t, errors.IsAlreadyExists(err))
	assert.Equal(t, "Endpoint ep already exists.", errors.Message(err))
}

func TestEndpointValidation(t *testing.T) {
	cases := map[string]func(c *driver.EndpointConfig){
		"bad name":            func(c *driver.EndpointConfig) { c.Name = "bad name" },
		"no health check":     func(c *driver.EndpointConfig) { c.Routing.HealthCheck = "" },
		"bad health check":    func(c *driver.EndpointConfig) { c.Routing.HealthCheck = "arn:aws:route53:::nope" },
		"no route":            func(c *driver.EndpointConfig) { c.Routing.SecondaryRoute = "" },
		"bad replication":     func(c *driver.EndpointConfig) { c.ReplicationState = "ON" },
		"one bus":             func(c *driver.EndpointConfig) { c.EventBusARNs = c.EventBusARNs[:1] },
		"three buses":         func(c *driver.EndpointConfig) { c.EventBusARNs = append(c.EventBusARNs, c.EventBusARNs[0]) },
		"bad bus arn":         func(c *driver.EndpointConfig) { c.EventBusARNs[1] = "arn:aws:sqs:us-west-2:123456789012:q" },
		"bus names differ":    func(c *driver.EndpointConfig) { c.EventBusARNs[1] = endpointBusARN("us-west-2", "b2") },
		"same region":         func(c *driver.EndpointConfig) { c.EventBusARNs[1] = endpointBusARN("us-east-1", "b1") },
		"no home region bus":  func(c *driver.EndpointConfig) { c.EventBusARNs[0] = endpointBusARN("eu-west-1", "b1") },
		"route not other bus": func(c *driver.EndpointConfig) { c.Routing.SecondaryRoute = "eu-west-1" },
		"route is home":       func(c *driver.EndpointConfig) { c.Routing.SecondaryRoute = "us-east-1" },
		"enabled without role": func(c *driver.EndpointConfig) {
			c.ReplicationState = replicationEnabled
		},
		"bad role": func(c *driver.EndpointConfig) { c.RoleARN = "arn:aws:iam::1:user/x" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := newTestMock()
			cfg := endpointConfig("ep")
			mutate(cfg)

			_, err := m.CreateEndpoint(context.Background(), cfg)
			assert.True(t, errors.IsInvalidArgument(err), "err = %v", err)
			assert.Equal(t, 0, m.endpoints.Len())
		})
	}
}

func TestEndpointValidationMessages(t *testing.T) {
	m, _ := newTestMock()
	cfg := endpointConfig("ep")
	cfg.Routing.HealthCheck = ""
	cfg.EventBusARNs = cfg.EventBusARNs[:1]

	_, err := m.CreateEndpoint(context.Background(), cfg)
	assert.Equal(t, "2 validation errors detected: "+
		"Value null at 'routingConfig.failoverConfig.primary.healthCheck' failed to satisfy constraint: Member must not be null; "+
		"Value '["+endpointBusARN("us-east-1", "b1")+"]' at 'eventBuses' failed to satisfy constraint: "+
		"Member must have length less than or equal to 2 and greater than or equal to 2", errors.Message(err))
}

func TestEndpointUpdateValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreateEndpoint(ctx, endpointConfig("ep"))
	require.NoError(t, err)

	_, err = m.UpdateEndpoint(ctx, &driver.EndpointUpdate{Name: "ep", ReplicationState: strPtr(replicationEnabled)})
	assert.True(t, errors.IsInvalidArgument(err), "ENABLED replication needs a role")

	_, err = m.UpdateEndpoint(ctx, &driver.EndpointUpdate{
		Name: "ep", EventBusARNs: []string{endpointBusARN("us-east-1", "b1")},
	})
	assert.True(t, errors.IsInvalidArgument(err))

	got, err := m.DescribeEndpoint(ctx, "ep", "")
	require.NoError(t, err)
	assert.Equal(t, replicationDisabled, got.ReplicationState, "a rejected update changes nothing")
	assert.Len(t, got.EventBusARNs, 2)

	_, err = m.UpdateEndpoint(ctx, &driver.EndpointUpdate{
		Name:    "ep",
		Routing: &driver.EndpointRouting{HealthCheck: "arn:aws:route53:::healthcheck/new", SecondaryRoute: "us-west-2"},
	})
	require.NoError(t, err)

	got, err = m.DescribeEndpoint(ctx, "ep", "")
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:route53:::healthcheck/new", got.Routing.HealthCheck)
}

func TestEndpointLimit(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	for i := range maxEndpoints {
		_, err := m.CreateEndpoint(ctx, endpointConfig("ep"+string(rune('a'+i/26))+string(rune('a'+i%26))))
		require.NoError(t, err)
	}

	_, err := m.CreateEndpoint(ctx, endpointConfig("over"))
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))
}

func TestListEndpointsFiltersAndPaging(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	for _, n := range []string{"b-2", "a-1", "a-2", "a-3"} {
		_, err := m.CreateEndpoint(ctx, endpointConfig(n))
		require.NoError(t, err)
	}

	page, next, err := m.ListEndpoints(ctx, driver.EndpointFilter{NamePrefix: "a-", MaxResults: 2})
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, "a-1", page[0].Name)
	assert.Equal(t, "ACTIVE", page[0].State)
	require.NotEmpty(t, next)

	page, next, err = m.ListEndpoints(ctx, driver.EndpointFilter{NamePrefix: "a-", MaxResults: 2, NextToken: next})
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, "a-3", page[0].Name)
	assert.Empty(t, next)

	all, _, err := m.ListEndpoints(ctx, driver.EndpointFilter{HomeRegion: "us-east-1"})
	require.NoError(t, err)
	assert.Len(t, all, 4)

	none, _, err := m.ListEndpoints(ctx, driver.EndpointFilter{HomeRegion: "eu-west-1"})
	require.NoError(t, err)
	assert.Empty(t, none)

	_, _, err = m.ListEndpoints(ctx, driver.EndpointFilter{MaxResults: 101})
	assert.True(t, errors.IsInvalidArgument(err))

	_, _, err = m.ListEndpoints(ctx, driver.EndpointFilter{NextToken: "garbage!"})
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestPutEventsThroughEndpoint(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	createTestBus(t, m, "b1")

	ep, err := m.CreateEndpoint(ctx, endpointConfig("ep"))
	require.NoError(t, err)

	res, err := m.PutEndpointEventEntries(ctx, ep.EndpointID, []driver.Event{
		{Source: "s", DetailType: "t", Detail: "{}"},
		{Source: "s", DetailType: "t", Detail: "{}", EventBus: endpointBusARN("us-west-2", "b1")},
		{Source: "s", DetailType: "t", Detail: "{}", EventBus: "b1"},
		{Source: "s", DetailType: "t", Detail: "{}", EventBus: "default"},
	})
	require.NoError(t, err)
	require.Len(t, res, 4)

	for _, r := range res {
		assert.NotEmpty(t, r.EventID)
	}

	b1, _ := m.buses.Get("b1")
	assert.Len(t, b1.events, 3, "unnamed and endpoint-bus entries land on the local primary bus")

	def, _ := m.buses.Get(defaultBusName)
	assert.Len(t, def.events, 1, "an entry naming another bus keeps it")

	_, err = m.PutEndpointEventEntries(ctx, "nope00000a.abc", []driver.Event{{Source: "s", DetailType: "t", Detail: "{}"}})
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, "Parameter EndpointId is not valid. Reason: Endpoint nope00000a.abc does not exist.", errors.Message(err))

	_, err = m.PutEndpointEventEntries(ctx, "no-dot", []driver.Event{{Source: "s", DetailType: "t", Detail: "{}"}})
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestSnapshotEndpoints(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	ep, err := src.CreateEndpoint(ctx, endpointConfig("ep"))
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(data2))

	got, err := dst.DescribeEndpoint(ctx, "ep", "")
	require.NoError(t, err)
	assert.Equal(t, ep.EndpointID, got.EndpointID)
	assert.Equal(t, ep.EventBusARNs, got.EventBusARNs)

	_, err = dst.PutEndpointEventEntries(ctx, ep.EndpointID, []driver.Event{{Source: "s", DetailType: "t", Detail: "{}"}})
	require.NoError(t, err, "a restored endpoint still routes PutEvents")
}

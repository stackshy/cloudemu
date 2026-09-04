package elbv2

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// mkListenerForTags creates a load balancer and listener, returning both ARNs.
func mkListenerForTags(t *testing.T, m *Mock, tags map[string]string) (lbARN, listenerARN string) {
	t.Helper()
	ctx := context.Background()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "tag-lb", Type: "application"})
	requireNoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, Tags: tags,
	})
	requireNoError(t, err)

	return lb.ARN, li.ARN
}

// TestCreateListenerStoresTags proves a listener created with Tags reports
// them back on the returned ListenerInfo and on a subsequent describe — before
// this fix ListenerInfo had no Tags field at all, so listener tags set at
// create time were silently dropped.
func TestCreateListenerStoresTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, listenerARN := mkListenerForTags(t, m, map[string]string{"env": "prod"})

	li, err := m.GetListener(ctx, listenerARN)
	requireNoError(t, err)
	assertEqual(t, "prod", li.Tags["env"])
}

// TestCreateRuleStoresTags and TestGetRule prove a rule created with Tags
// reports them back via the new GetRule accessor — required because ELBv2
// DescribeTags accepts listener-rule ARNs directly, and DescribeRules only
// looks up by parent listener ARN.
func TestCreateRuleStoresTags(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, listenerARN := mkListenerForTags(t, m, nil)

	rule, err := m.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: listenerARN,
		Priority:    1,
		Conditions:  []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
		Tags:        map[string]string{"team": "platform"},
	})
	requireNoError(t, err)
	assertEqual(t, "platform", rule.Tags["team"])

	got, err := m.GetRule(ctx, rule.ARN)
	requireNoError(t, err)
	assertEqual(t, "platform", got.Tags["team"])
}

// TestGetRuleNotFound proves an unknown rule ARN reports NotFound rather than
// panicking or returning a zero-value rule.
func TestGetRuleNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.GetRule(context.Background(), "arn:nope")
	if err == nil {
		t.Fatal("GetRule(unknown ARN) = nil error, want NotFound")
	}
}

// TestAddResourceTagsAppliesToListener and TestAddResourceTagsAppliesToRule
// prove AddResourceTags/RemoveResourceTags — generalized to operate over any
// of the four taggable ELBv2 resource kinds — reach listeners and rules, not
// just load balancers and target groups.
func TestAddResourceTagsAppliesToListener(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, listenerARN := mkListenerForTags(t, m, nil)

	requireNoError(t, m.AddResourceTags(ctx, listenerARN, map[string]string{"owner": "team-a"}))

	li, err := m.GetListener(ctx, listenerARN)
	requireNoError(t, err)
	assertEqual(t, "team-a", li.Tags["owner"])

	requireNoError(t, m.RemoveResourceTags(ctx, listenerARN, []string{"owner"}))

	li, err = m.GetListener(ctx, listenerARN)
	requireNoError(t, err)

	if _, ok := li.Tags["owner"]; ok {
		t.Fatalf("tag survived RemoveResourceTags: %v", li.Tags)
	}
}

func TestAddResourceTagsAppliesToRule(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, listenerARN := mkListenerForTags(t, m, nil)

	rule, err := m.CreateRule(ctx, driver.RuleConfig{
		ListenerARN: listenerARN,
		Priority:    5,
		Conditions:  []driver.RuleCondition{{Field: "path-pattern", Values: []string{"/x"}}},
	})
	requireNoError(t, err)

	requireNoError(t, m.AddResourceTags(ctx, rule.ARN, map[string]string{"owner": "team-b"}))

	got, err := m.GetRule(ctx, rule.ARN)
	requireNoError(t, err)
	assertEqual(t, "team-b", got.Tags["owner"])

	requireNoError(t, m.RemoveResourceTags(ctx, rule.ARN, []string{"owner"}))

	got, err = m.GetRule(ctx, rule.ARN)
	requireNoError(t, err)

	if _, ok := got.Tags["owner"]; ok {
		t.Fatalf("tag survived RemoveResourceTags: %v", got.Tags)
	}
}

// TestAddResourceTagsUnknownARNIsNoOp proves AddResourceTags/RemoveResourceTags
// still tolerate an ARN that resolves against none of the four resource kinds,
// matching AWS's tolerance for a mixed multi-resource-kind call.
func TestAddResourceTagsUnknownARNIsNoOp(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.AddResourceTags(ctx, "arn:nope", map[string]string{"k": "v"}))
	requireNoError(t, m.RemoveResourceTags(ctx, "arn:nope", []string{"k"}))
}

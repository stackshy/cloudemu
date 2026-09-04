package elbv2

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// mkListenerOfType creates a load balancer of the given type plus one
// listener on it, returning the listener ARN.
func mkListenerOfType(t *testing.T, m *Mock, lbType string) string {
	t.Helper()
	ctx := context.Background()

	lb, err := m.CreateLoadBalancer(ctx, driver.LBConfig{Name: "attr-lb-" + lbType, Type: lbType})
	requireNoError(t, err)

	li, err := m.CreateListener(ctx, driver.ListenerConfig{LBARN: lb.ARN, Protocol: "TCP", Port: 80})
	requireNoError(t, err)

	return li.ARN
}

// TestGetListenerAttributesDefaultsByLBType proves DescribeListenerAttributes
// derives its defaults from the parent load balancer's type: a Network or
// Gateway Load Balancer listener defaults tcp.idle_timeout.seconds to 350, per
// the ListenerAttribute API reference, while an Application Load Balancer
// listener has no documented default and starts with an empty attribute set.
// Before this fix the emulator had no listener attribute surface at all.
func TestGetListenerAttributesDefaultsByLBType(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	nlbListener := mkListenerOfType(t, m, "network")
	attrs, err := m.GetListenerAttributes(ctx, nlbListener)
	requireNoError(t, err)
	assertEqual(t, "350", attrs["tcp.idle_timeout.seconds"])

	gwlbListener := mkListenerOfType(t, m, "gateway")
	attrs, err = m.GetListenerAttributes(ctx, gwlbListener)
	requireNoError(t, err)
	assertEqual(t, "350", attrs["tcp.idle_timeout.seconds"])

	albListener := mkListenerOfType(t, m, "application")
	attrs, err = m.GetListenerAttributes(ctx, albListener)
	requireNoError(t, err)
	if len(attrs) != 0 {
		t.Fatalf("application listener attributes = %v, want empty", attrs)
	}
}

// TestModifyListenerAttributesMerges proves ModifyListenerAttributes merges
// updates into the stored overrides (rather than replacing the whole set) and
// that a subsequent Get reflects the merge.
func TestModifyListenerAttributesMerges(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	listenerARN := mkListenerOfType(t, m, "network")

	merged, err := m.ModifyListenerAttributes(ctx, listenerARN, map[string]string{
		"tcp.idle_timeout.seconds": "60",
	})
	requireNoError(t, err)
	assertEqual(t, "60", merged["tcp.idle_timeout.seconds"])

	got, err := m.GetListenerAttributes(ctx, listenerARN)
	requireNoError(t, err)
	assertEqual(t, "60", got["tcp.idle_timeout.seconds"])
}

// TestGetListenerAttributesUnknownARN proves an unknown listener ARN reports
// NotFound.
func TestGetListenerAttributesUnknownARN(t *testing.T) {
	m := newTestMock()

	if _, err := m.GetListenerAttributes(context.Background(), "arn:nope"); err == nil {
		t.Fatal("GetListenerAttributes(unknown ARN) = nil error, want NotFound")
	}

	if _, err := m.ModifyListenerAttributes(context.Background(), "arn:nope", map[string]string{"k": "v"}); err == nil {
		t.Fatal("ModifyListenerAttributes(unknown ARN) = nil error, want NotFound")
	}
}

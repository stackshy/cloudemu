package globalaccelerator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/globalaccelerator"
	driver "github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

func newMock() *globalaccelerator.Mock {
	return globalaccelerator.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error, op string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: unexpected error: %v", op, err)
	}
}

func assertException(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error tagged %s, got nil", want)
	}

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not a driver.APIError", err)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}
}

func createAccelerator(t *testing.T, m *globalaccelerator.Mock, name string) *driver.Accelerator {
	t.Helper()

	a, err := m.CreateAccelerator(context.Background(), &driver.CreateAcceleratorInput{Name: name})
	requireNoError(t, err, "CreateAccelerator")

	return a
}

func TestCreateAcceleratorComputedFields(t *testing.T) {
	m := newMock()
	a := createAccelerator(t, m, "web")

	if a.Status != "DEPLOYED" {
		t.Fatalf("status = %q, want DEPLOYED", a.Status)
	}

	if a.IPAddressType != "IPV4" {
		t.Fatalf("ip address type = %q, want IPV4", a.IPAddressType)
	}

	const wantPrefix = "arn:aws:globalaccelerator::123456789012:accelerator/"
	if len(a.AcceleratorArn) <= len(wantPrefix) || a.AcceleratorArn[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("arn = %q, want prefix %q (global service: empty region)", a.AcceleratorArn, wantPrefix)
	}

	if len(a.IPSets) != 1 || len(a.IPSets[0].IPAddresses) != 2 {
		t.Fatalf("want one IPv4 set of two addresses, got %+v", a.IPSets)
	}

	if a.DNSName == "" || a.DualStackDNSName == "" {
		t.Fatalf("dns names must be set: %q / %q", a.DNSName, a.DualStackDNSName)
	}

	if !a.Enabled {
		t.Fatalf("accelerator should default to enabled")
	}
}

func TestComputedFieldStability(t *testing.T) {
	m := newMock()
	a := createAccelerator(t, m, "web")

	for i := 0; i < 3; i++ {
		got, err := m.DescribeAccelerator(context.Background(), a.AcceleratorArn)
		requireNoError(t, err, "DescribeAccelerator")

		if got.DNSName != a.DNSName || got.DualStackDNSName != a.DualStackDNSName {
			t.Fatalf("dns drift on read %d", i)
		}

		if got.IPSets[0].IPAddresses[0] != a.IPSets[0].IPAddresses[0] ||
			got.IPSets[0].IPAddresses[1] != a.IPSets[0].IPAddresses[1] {
			t.Fatalf("ip set drift on read %d", i)
		}

		if !got.CreatedTime.Equal(a.CreatedTime) {
			t.Fatalf("created time drift on read %d", i)
		}
	}
}

func TestReadsDeepCopy(t *testing.T) {
	m := newMock()
	a := createAccelerator(t, m, "web")

	// Mutating a read result must not alter stored state.
	a.IPSets[0].IPAddresses[0] = "0.0.0.0"

	got, err := m.DescribeAccelerator(context.Background(), a.AcceleratorArn)
	requireNoError(t, err, "DescribeAccelerator")

	if got.IPSets[0].IPAddresses[0] == "0.0.0.0" {
		t.Fatalf("stored ip set was aliased through a read result")
	}
}

func TestDeleteAcceleratorGuards(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	a := createAccelerator(t, m, "web")

	// Enabled accelerator cannot be deleted.
	assertException(t, m.DeleteAccelerator(ctx, a.AcceleratorArn), driver.ExAcceleratorNotDisabled)

	// Disable, add a listener, delete rejected with AssociatedListenerFound.
	disabled := false
	_, err := m.UpdateAccelerator(ctx, &driver.UpdateAcceleratorInput{AcceleratorArn: a.AcceleratorArn, Enabled: &disabled})
	requireNoError(t, err, "UpdateAccelerator")

	l, err := m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: a.AcceleratorArn,
		Protocol:       "TCP",
		PortRanges:     []driver.PortRange{{FromPort: 80, ToPort: 80}},
	})
	requireNoError(t, err, "CreateListener")

	assertException(t, m.DeleteAccelerator(ctx, a.AcceleratorArn), driver.ExAssociatedListenerFound)

	// Listener with an endpoint group cannot be deleted.
	_, err = m.CreateEndpointGroup(ctx, &driver.CreateEndpointGroupInput{
		ListenerArn:         l.ListenerArn,
		EndpointGroupRegion: "us-east-1",
	})
	requireNoError(t, err, "CreateEndpointGroup")

	assertException(t, m.DeleteListener(ctx, l.ListenerArn), driver.ExAssociatedEndpointGroup)
}

func TestNotFoundExceptions(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.DescribeAccelerator(ctx, "arn:aws:globalaccelerator::123456789012:accelerator/missing")
	assertException(t, err, driver.ExAcceleratorNotFound)

	_, err = m.DescribeListener(ctx, "arn:aws:globalaccelerator::123456789012:accelerator/x/listener/missing")
	assertException(t, err, driver.ExListenerNotFound)

	_, err = m.DescribeEndpointGroup(ctx,
		"arn:aws:globalaccelerator::123456789012:accelerator/x/listener/y/endpoint-group/missing")
	assertException(t, err, driver.ExEndpointGroupNotFound)

	_, err = m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: "arn:aws:globalaccelerator::123456789012:accelerator/missing",
		Protocol:       "TCP",
		PortRanges:     []driver.PortRange{{FromPort: 80, ToPort: 80}},
	})
	assertException(t, err, driver.ExAcceleratorNotFound)
}

func TestEndpointGroupDefaults(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	a := createAccelerator(t, m, "web")

	l, err := m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: a.AcceleratorArn,
		Protocol:       "TCP",
		PortRanges:     []driver.PortRange{{FromPort: 443, ToPort: 443}},
	})
	requireNoError(t, err, "CreateListener")

	g, err := m.CreateEndpointGroup(ctx, &driver.CreateEndpointGroupInput{
		ListenerArn:         l.ListenerArn,
		EndpointGroupRegion: "eu-west-1",
	})
	requireNoError(t, err, "CreateEndpointGroup")

	if g.TrafficDialPercentage == nil || *g.TrafficDialPercentage != 100 {
		t.Fatalf("traffic dial = %v, want 100", g.TrafficDialPercentage)
	}

	if g.HealthCheckPort == nil || *g.HealthCheckPort != 443 {
		t.Fatalf("health check port = %v, want 443 (listener port default)", g.HealthCheckPort)
	}

	if g.HealthCheckProtocol != "TCP" {
		t.Fatalf("health check protocol = %q, want TCP", g.HealthCheckProtocol)
	}

	if g.HealthCheckIntervalSeconds == nil || *g.HealthCheckIntervalSeconds != 30 {
		t.Fatalf("interval = %v, want 30", g.HealthCheckIntervalSeconds)
	}

	if g.ThresholdCount == nil || *g.ThresholdCount != 3 {
		t.Fatalf("threshold = %v, want 3", g.ThresholdCount)
	}
}

func TestPointerRoundTripZeroValues(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	a := createAccelerator(t, m, "web")

	l, err := m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: a.AcceleratorArn,
		Protocol:       "TCP",
		PortRanges:     []driver.PortRange{{FromPort: 80, ToPort: 80}},
	})
	requireNoError(t, err, "CreateListener")

	zeroDial := float64(0)
	zeroPort := int32(0)
	falsePreserve := false
	zeroWeight := int32(0)

	g, err := m.CreateEndpointGroup(ctx, &driver.CreateEndpointGroupInput{
		ListenerArn:           l.ListenerArn,
		EndpointGroupRegion:   "us-east-1",
		TrafficDialPercentage: &zeroDial,
		HealthCheckPort:       &zeroPort,
		EndpointConfigurations: []driver.EndpointConfiguration{{
			EndpointID:                  "i-123",
			Weight:                      &zeroWeight,
			ClientIPPreservationEnabled: &falsePreserve,
		}},
	})
	requireNoError(t, err, "CreateEndpointGroup")

	if g.TrafficDialPercentage == nil || *g.TrafficDialPercentage != 0 {
		t.Fatalf("explicit zero traffic dial must round-trip, got %v", g.TrafficDialPercentage)
	}

	if g.HealthCheckPort == nil || *g.HealthCheckPort != 0 {
		t.Fatalf("explicit zero health check port must round-trip, got %v", g.HealthCheckPort)
	}

	ep := g.EndpointDescriptions[0]
	if ep.Weight == nil || *ep.Weight != 0 {
		t.Fatalf("explicit zero weight must round-trip, got %v", ep.Weight)
	}

	if ep.ClientIPPreservationEnabled == nil || *ep.ClientIPPreservationEnabled != false {
		t.Fatalf("explicit false client-ip-preservation must round-trip, got %v", ep.ClientIPPreservationEnabled)
	}
}

func TestTagsLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	a := createAccelerator(t, m, "web")

	requireNoError(t, m.TagResource(ctx, a.AcceleratorArn, map[string]string{"env": "prod", "team": "net"}), "TagResource")

	tags, err := m.ListTagsForResource(ctx, a.AcceleratorArn)
	requireNoError(t, err, "ListTagsForResource")

	if tags["env"] != "prod" || tags["team"] != "net" {
		t.Fatalf("tags = %+v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, a.AcceleratorArn, []string{"team"}), "UntagResource")

	tags, err = m.ListTagsForResource(ctx, a.AcceleratorArn)
	requireNoError(t, err, "ListTagsForResource")

	if _, ok := tags["team"]; ok {
		t.Fatalf("team tag should be removed: %+v", tags)
	}
}

func TestListScopingAndPagination(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	a1 := createAccelerator(t, m, "a1")
	a2 := createAccelerator(t, m, "a2")

	l1, err := m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: a1.AcceleratorArn, Protocol: "TCP", PortRanges: []driver.PortRange{{FromPort: 80, ToPort: 80}},
	})
	requireNoError(t, err, "CreateListener a1")

	_, err = m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: a2.AcceleratorArn, Protocol: "TCP", PortRanges: []driver.PortRange{{FromPort: 80, ToPort: 80}},
	})
	requireNoError(t, err, "CreateListener a2")

	ls, _, err := m.ListListeners(ctx, a1.AcceleratorArn, driver.Page{})
	requireNoError(t, err, "ListListeners")

	if len(ls) != 1 || ls[0].ListenerArn != l1.ListenerArn {
		t.Fatalf("ListListeners must scope to accelerator a1, got %+v", ls)
	}

	accs, _, err := m.ListAccelerators(ctx, driver.Page{})
	requireNoError(t, err, "ListAccelerators")

	if len(accs) != 2 {
		t.Fatalf("want 2 accelerators, got %d", len(accs))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	a := createAccelerator(t, m, "web")

	l, err := m.CreateListener(ctx, &driver.CreateListenerInput{
		AcceleratorArn: a.AcceleratorArn, Protocol: "TCP", PortRanges: []driver.PortRange{{FromPort: 80, ToPort: 80}},
	})
	requireNoError(t, err, "CreateListener")

	_, err = m.CreateEndpointGroup(ctx, &driver.CreateEndpointGroupInput{
		ListenerArn: l.ListenerArn, EndpointGroupRegion: "us-east-1",
	})
	requireNoError(t, err, "CreateEndpointGroup")

	_, err = m.UpdateAcceleratorAttributes(ctx, &driver.UpdateAcceleratorAttributesInput{
		AcceleratorArn: a.AcceleratorArn, FlowLogsEnabled: boolPtr(true),
	})
	requireNoError(t, err, "UpdateAcceleratorAttributes")

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "Snapshot")

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data), "Restore")

	got, err := restored.DescribeAccelerator(ctx, a.AcceleratorArn)
	requireNoError(t, err, "DescribeAccelerator after restore")

	if got.DNSName != a.DNSName {
		t.Fatalf("dns name not preserved across snapshot/restore")
	}

	attr, err := restored.DescribeAcceleratorAttributes(ctx, a.AcceleratorArn)
	requireNoError(t, err, "DescribeAcceleratorAttributes after restore")

	if !attr.FlowLogsEnabled {
		t.Fatalf("attributes not preserved across snapshot/restore")
	}
}

func boolPtr(b bool) *bool { return &b }

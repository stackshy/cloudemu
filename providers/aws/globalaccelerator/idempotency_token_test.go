package globalaccelerator_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws/globalaccelerator"
	driver "github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

func TestCreateAcceleratorIdempotencyToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "tok-1"}

	first, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator")

	retry, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator retry")

	if retry.AcceleratorArn != first.AcceleratorArn {
		t.Fatalf("same-token retry arn = %s, want original %s", retry.AcceleratorArn, first.AcceleratorArn)
	}

	in.IdempotencyToken = "tok-2"

	other, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator new token")

	if other.AcceleratorArn == first.AcceleratorArn {
		t.Fatalf("different token must mint a new accelerator, got %s again", other.AcceleratorArn)
	}

	list, _, err := m.ListAccelerators(ctx, driver.Page{})
	requireNoError(t, err, "ListAccelerators")

	if len(list) != 2 {
		t.Fatalf("accelerator count = %d, want 2 (no duplicate on retry)", len(list))
	}
}

func disableAndDelete(t *testing.T, m *globalaccelerator.Mock, arn string) {
	t.Helper()

	off := false
	_, err := m.UpdateAccelerator(context.Background(), &driver.UpdateAcceleratorInput{AcceleratorArn: arn, Enabled: &off})
	requireNoError(t, err, "UpdateAccelerator disable")
	requireNoError(t, m.DeleteAccelerator(context.Background(), arn), "DeleteAccelerator")
}

func TestCreateAcceleratorTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "g1"}

	first, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator")
	disableAndDelete(t, m, first.AcceleratorArn)

	again, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator after delete")

	if again.AcceleratorArn == first.AcceleratorArn {
		t.Fatalf("same-token create after delete replayed the deleted accelerator %s", first.AcceleratorArn)
	}

	_, err = m.DescribeAccelerator(ctx, again.AcceleratorArn)
	requireNoError(t, err, "DescribeAccelerator")
}

func TestCreateAcceleratorTokenAfterUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "g1"}

	first, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator")

	name, off := "renamed", false
	_, err = m.UpdateAccelerator(ctx, &driver.UpdateAcceleratorInput{AcceleratorArn: first.AcceleratorArn, Name: &name, Enabled: &off})
	requireNoError(t, err, "UpdateAccelerator")

	retry, err := m.CreateAccelerator(ctx, in)
	requireNoError(t, err, "CreateAccelerator retry")

	if retry.AcceleratorArn != first.AcceleratorArn || retry.Name != "renamed" || retry.Enabled {
		t.Fatalf("replay = %s name=%q enabled=%v, want live %s name=renamed enabled=false",
			retry.AcceleratorArn, retry.Name, retry.Enabled, first.AcceleratorArn)
	}
}

func listenerInput(accArn, token string) *driver.CreateListenerInput {
	return &driver.CreateListenerInput{
		AcceleratorArn:   accArn,
		PortRanges:       []driver.PortRange{{FromPort: 80, ToPort: 80}},
		Protocol:         "TCP",
		IdempotencyToken: token,
	}
}

func TestCreateListenerIdempotencyToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	acc, err := m.CreateAccelerator(ctx, &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "a"})
	requireNoError(t, err, "CreateAccelerator")

	first, err := m.CreateListener(ctx, listenerInput(acc.AcceleratorArn, "l1"))
	requireNoError(t, err, "CreateListener")

	retry, err := m.CreateListener(ctx, listenerInput(acc.AcceleratorArn, "l1"))
	requireNoError(t, err, "CreateListener retry")

	if retry.ListenerArn != first.ListenerArn {
		t.Fatalf("same-token retry = %s, want %s", retry.ListenerArn, first.ListenerArn)
	}

	requireNoError(t, m.DeleteListener(ctx, first.ListenerArn), "DeleteListener")

	again, err := m.CreateListener(ctx, listenerInput(acc.AcceleratorArn, "l1"))
	requireNoError(t, err, "CreateListener after delete")

	if again.ListenerArn == first.ListenerArn {
		t.Fatalf("same-token create after delete replayed the deleted listener %s", first.ListenerArn)
	}

	list, _, err := m.ListListeners(ctx, acc.AcceleratorArn, driver.Page{})
	requireNoError(t, err, "ListListeners")

	if len(list) != 1 {
		t.Fatalf("listener count = %d, want 1", len(list))
	}
}

func egInput(listenerArn, token string) *driver.CreateEndpointGroupInput {
	return &driver.CreateEndpointGroupInput{ListenerArn: listenerArn, EndpointGroupRegion: "us-east-1", IdempotencyToken: token}
}

func TestCreateEndpointGroupIdempotencyToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	acc, err := m.CreateAccelerator(ctx, &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "a"})
	requireNoError(t, err, "CreateAccelerator")

	l, err := m.CreateListener(ctx, listenerInput(acc.AcceleratorArn, "l"))
	requireNoError(t, err, "CreateListener")

	first, err := m.CreateEndpointGroup(ctx, egInput(l.ListenerArn, "e1"))
	requireNoError(t, err, "CreateEndpointGroup")

	retry, err := m.CreateEndpointGroup(ctx, egInput(l.ListenerArn, "e1"))
	requireNoError(t, err, "CreateEndpointGroup retry")

	if retry.EndpointGroupArn != first.EndpointGroupArn {
		t.Fatalf("same-token retry = %s, want %s", retry.EndpointGroupArn, first.EndpointGroupArn)
	}

	requireNoError(t, m.DeleteEndpointGroup(ctx, first.EndpointGroupArn), "DeleteEndpointGroup")

	again, err := m.CreateEndpointGroup(ctx, egInput(l.ListenerArn, "e1"))
	requireNoError(t, err, "CreateEndpointGroup after delete")

	if again.EndpointGroupArn == first.EndpointGroupArn {
		t.Fatalf("same-token create after delete replayed the deleted endpoint group %s", first.EndpointGroupArn)
	}
}

func TestGlobalAcceleratorTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	const n = 20

	arns := make([][3]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			arns[i], errs[i] = createChain(ctx, m)
		}()
	}

	close(start)
	wg.Wait()

	for i := range n {
		requireNoError(t, errs[i], "create chain")

		if arns[i] != arns[0] {
			t.Fatalf("call %d = %v, want the single chain %v", i, arns[i], arns[0])
		}
	}

	accs, _, err := m.ListAccelerators(ctx, driver.Page{})
	requireNoError(t, err, "ListAccelerators")

	listeners, _, err := m.ListListeners(ctx, arns[0][0], driver.Page{})
	requireNoError(t, err, "ListListeners")

	groups, _, err := m.ListEndpointGroups(ctx, arns[0][1], driver.Page{})
	requireNoError(t, err, "ListEndpointGroups")

	if len(accs) != 1 || len(listeners) != 1 || len(groups) != 1 {
		t.Fatalf("accelerators=%d listeners=%d groups=%d, want exactly 1 each", len(accs), len(listeners), len(groups))
	}
}

// createChain creates accelerator -> listener -> endpoint group, each with a
// fixed token, returning the three ARNs.
func createChain(ctx context.Context, m *globalaccelerator.Mock) ([3]string, error) {
	acc, err := m.CreateAccelerator(ctx, &driver.CreateAcceleratorInput{Name: "acc", IdempotencyToken: "burst"})
	if err != nil {
		return [3]string{}, err
	}

	l, err := m.CreateListener(ctx, listenerInput(acc.AcceleratorArn, "burst"))
	if err != nil {
		return [3]string{}, err
	}

	g, err := m.CreateEndpointGroup(ctx, egInput(l.ListenerArn, "burst"))
	if err != nil {
		return [3]string{}, err
	}

	return [3]string{acc.AcceleratorArn, l.ListenerArn, g.EndpointGroupArn}, nil
}

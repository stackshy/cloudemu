package cloudrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

func TestInvokeNoHandlerEchoesBody(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	resp, err := m.Invoke(ctx, "web", driver.InvokeRequest{
		Method:  "POST",
		Path:    "/",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    []byte(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("statusCode = %d, want 200", resp.StatusCode)
	}

	if string(resp.Body) != `{"hello":"world"}` {
		t.Fatalf("body = %q, want echo", resp.Body)
	}

	if got := resp.Headers["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("content-type = %v, want echoed application/json", got)
	}
}

func TestInvokeNoHandlerNoBodyReturnsGreeting(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	resp, err := m.Invoke(ctx, "web", driver.InvokeRequest{Method: "GET", Path: "/"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("statusCode = %d, want 200", resp.StatusCode)
	}

	if len(resp.Body) == 0 {
		t.Fatal("body empty, want a canned greeting")
	}
}

func TestInvokeUnknownServiceNotFound(t *testing.T) {
	m := newMock(t, nil)

	if _, err := m.Invoke(context.Background(), "missing", driver.InvokeRequest{}); err == nil {
		t.Fatal("Invoke: want NotFound error for unknown service")
	}
}

func TestInvokeRegisteredHandlerRuns(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	var gotReq driver.InvokeRequest

	m.RegisterHandler("web", func(_ context.Context, req driver.InvokeRequest) (driver.InvokeResponse, error) {
		gotReq = req

		return driver.InvokeResponse{StatusCode: 201, Body: []byte("handled")}, nil
	})

	resp, err := m.Invoke(ctx, "web", driver.InvokeRequest{Method: "PUT", Path: "/items/1", Body: []byte("in")})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if resp.StatusCode != 201 || string(resp.Body) != "handled" {
		t.Fatalf("resp = %+v", resp)
	}

	if gotReq.Method != "PUT" || gotReq.Path != "/items/1" || string(gotReq.Body) != "in" {
		t.Fatalf("handler saw = %+v", gotReq)
	}
}

func TestInvokeRegisteredHandlerErrorReturns500(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	m.RegisterHandler("web", func(context.Context, driver.InvokeRequest) (driver.InvokeResponse, error) {
		return driver.InvokeResponse{}, errors.New("boom")
	})

	resp, err := m.Invoke(ctx, "web", driver.InvokeRequest{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if resp.StatusCode != 500 {
		t.Fatalf("statusCode = %d, want 500", resp.StatusCode)
	}
}

func TestRegisterHandlerNilClearsHandler(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	m.RegisterHandler("web", func(context.Context, driver.InvokeRequest) (driver.InvokeResponse, error) {
		return driver.InvokeResponse{StatusCode: 201}, nil
	})
	m.RegisterHandler("web", nil)

	resp, err := m.Invoke(ctx, "web", driver.InvokeRequest{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("statusCode = %d, want 200 (echo stub after clearing handler)", resp.StatusCode)
	}
}

// TestDeleteServiceEvictsHandler covers a redeploy flow: RegisterHandler on a
// service, DeleteService it, then CreateService again under the same id. The
// new service must get the documented no-handler echo stub, not silently
// inherit the deleted deployment's handler (handlers are keyed by bare
// service id, independent of the services store DeleteService clears).
func TestDeleteServiceEvictsHandler(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	m.RegisterHandler("web", func(context.Context, driver.InvokeRequest) (driver.InvokeResponse, error) {
		return driver.InvokeResponse{StatusCode: 201, Body: []byte("stale handler")}, nil
	})

	if err := m.DeleteService(ctx, "web"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if _, err := m.CreateService(ctx, svcCfg()); err != nil {
		t.Fatalf("CreateService (redeploy): %v", err)
	}

	resp, err := m.Invoke(ctx, "web", driver.InvokeRequest{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if resp.StatusCode != 200 || string(resp.Body) == "stale handler" {
		t.Fatalf("resp = %+v, want the echo stub (200, greeting), not the stale handler's response", resp)
	}
}

package cloudrun

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

const (
	invokeStatusOK    = 200
	invokeStatusError = 500
	contentTypeHeader = "Content-Type"
	textPlainHeader   = "text/plain; charset=utf-8"
)

// Invoke executes the named service against req. When a Go handler was
// registered for the service via RegisterHandler it runs; otherwise Invoke
// returns a canned/echo stub response — 200 echoing req.Body back when the
// caller sent one, or a short greeting naming the service when the request
// carried no body. This mirrors the no-handler echo stub the Cloud Functions
// and Lambda mocks return, since CloudEmu has no runtime for the container
// images a real Cloud Run service would execute.
//
//nolint:gocritic // hugeParam: req is passed by value to satisfy the CloudRun driver interface.
func (m *Mock) Invoke(ctx context.Context, name string, req driver.InvokeRequest) (*driver.InvokeResponse, error) {
	id := lastSegment(name)

	m.mu.Lock()
	_, ok := m.services.Get(id)
	m.mu.Unlock()

	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "service %q not found", id)
	}

	m.handlersMu.RLock()
	h := m.handlers[id]
	m.handlersMu.RUnlock()

	if h == nil {
		resp := stubInvoke(id, &req)

		return &resp, nil
	}

	resp, err := h(ctx, req)
	if err != nil {
		return &driver.InvokeResponse{StatusCode: invokeStatusError, Body: []byte(err.Error())}, nil
	}

	return &resp, nil
}

// RegisterHandler plugs a Go handler in for the named service id, standing in
// for its container image. Registering nil clears a previously registered
// handler, reverting the service to the echo stub.
func (m *Mock) RegisterHandler(name string, handler driver.HandlerFunc) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()

	if handler == nil {
		delete(m.handlers, name)
		return
	}

	m.handlers[name] = handler
}

// stubInvoke builds the no-handler canned response: an echo of the request
// body (preserving its Content-Type when the caller sent one) when a body was
// sent, or a short plain-text greeting naming the service otherwise.
func stubInvoke(name string, req *driver.InvokeRequest) driver.InvokeResponse {
	if len(req.Body) > 0 {
		headers := map[string][]string{}
		if ct, ok := req.Headers[contentTypeHeader]; ok && len(ct) > 0 {
			headers[contentTypeHeader] = []string{ct[0]}
		}

		return driver.InvokeResponse{StatusCode: invokeStatusOK, Headers: headers, Body: req.Body}
	}

	return driver.InvokeResponse{
		StatusCode: invokeStatusOK,
		Headers:    map[string][]string{contentTypeHeader: {textPlainHeader}},
		Body:       []byte("Hello from Cloud Run service \"" + name + "\"!\n"),
	}
}

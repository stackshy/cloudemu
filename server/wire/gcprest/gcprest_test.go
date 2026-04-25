package gcprest_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

func TestParsePathRejectsNonComputePrefix(t *testing.T) {
	if _, ok := gcprest.ParsePath("/storage/v1/b/x"); ok {
		t.Error("expected ok=false for non-compute path")
	}
}

func TestParsePathProjectOnly(t *testing.T) {
	rp, ok := gcprest.ParsePath("/compute/v1/projects/p")
	if !ok || rp.Project != "p" {
		t.Errorf("rp=%+v ok=%v", rp, ok)
	}
}

func TestParsePathZoneScope(t *testing.T) {
	rp, ok := gcprest.ParsePath("/compute/v1/projects/p/zones/z/instances/inst")
	if !ok {
		t.Fatal("expected ok=true")
	}

	if rp.Scope != "zones" || rp.ScopeName != "z" || rp.ResourceType != "instances" || rp.ResourceName != "inst" {
		t.Errorf("rp=%+v", rp)
	}
}

func TestParsePathGlobalScope(t *testing.T) {
	rp, ok := gcprest.ParsePath("/compute/v1/projects/p/global/networks/default")
	if !ok || rp.Scope != "global" || rp.ResourceName != "default" {
		t.Errorf("rp=%+v", rp)
	}
}

func TestParsePathAction(t *testing.T) {
	rp, ok := gcprest.ParsePath("/compute/v1/projects/p/zones/z/instances/inst/start")
	if !ok || rp.Action != "start" {
		t.Errorf("rp=%+v", rp)
	}
}

func TestParsePathRejectsBadShapes(t *testing.T) {
	cases := []string{
		"/compute/v1/foo",
		"/compute/v1/projects",
		"/compute/v1/projects/p/zones",        // missing zone name
		"/compute/v1/projects/p/unknownscope", // unrecognised scope keyword
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, ok := gcprest.ParsePath(c); ok {
				t.Errorf("expected ok=false for %q", c)
			}
		})
	}
}

func TestSelfLinkScopes(t *testing.T) {
	zonal := gcprest.SelfLink("http://x", "p", "zones", "z", "instances", "inst")
	if !strings.HasSuffix(zonal, "/projects/p/zones/z/instances/inst") {
		t.Errorf("zonal=%s", zonal)
	}

	global := gcprest.SelfLink("http://x", "p", "global", "", "networks", "default")
	if !strings.HasSuffix(global, "/projects/p/global/networks/default") {
		t.Errorf("global=%s", global)
	}
}

func TestNewDoneOperationFields(t *testing.T) {
	op := gcprest.NewDoneOperation("http://h", "p", "zones", "z", "instances", "vm1", "insert")

	if op.Status != "DONE" {
		t.Errorf("status=%s", op.Status)
	}

	if op.Progress != 100 {
		t.Errorf("progress=%d", op.Progress)
	}

	if !strings.HasSuffix(op.TargetLink, "/instances/vm1") {
		t.Errorf("targetLink=%s", op.TargetLink)
	}

	if !strings.Contains(op.Zone, "/zones/z") {
		t.Errorf("zone=%s", op.Zone)
	}
}

func TestNewDoneOperationGlobal(t *testing.T) {
	op := gcprest.NewDoneOperation("http://h", "p", "global", "", "networks", "default", "insert")

	if op.Zone != "" {
		t.Errorf("expected empty Zone for global op, got %s", op.Zone)
	}
}

func TestWriteCErrMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"NotFound", cerrors.New(cerrors.NotFound, "missing"), http.StatusNotFound, "notFound"},
		{"AlreadyExists", cerrors.New(cerrors.AlreadyExists, "dup"), http.StatusConflict, "alreadyExists"},
		{"InvalidArgument", cerrors.New(cerrors.InvalidArgument, "bad"), http.StatusBadRequest, "invalid"},
		{"FailedPrecondition", cerrors.New(cerrors.FailedPrecondition, "fp"), http.StatusConflict, "conditionNotMet"},
		{"Unknown", errors.New("boom"), http.StatusInternalServerError, "internalError"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			gcprest.WriteCErr(rec, c.err)

			if rec.Code != c.status {
				t.Errorf("status=%d want %d", rec.Code, c.status)
			}

			if !strings.Contains(rec.Body.String(), c.reason) {
				t.Errorf("body=%q does not contain %q", rec.Body.String(), c.reason)
			}
		})
	}
}

func TestDecodeJSONInvalid(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{not json`))

	var out struct {
		Foo string `json:"foo"`
	}

	if gcprest.DecodeJSON(rec, r, &out) {
		t.Error("expected DecodeJSON to return false")
	}

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rec.Code)
	}
}

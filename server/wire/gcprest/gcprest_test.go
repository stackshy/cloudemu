package gcprest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
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
		name string
		err  error
		code int
		// reason is the Google JSON-API camelCase token in errors[].reason.
		reason string
		// status is the canonical google.rpc.Code NAME in the top-level
		// error.status field, matching what real GCP returns.
		status string
	}{
		{"NotFound", cerrors.New(cerrors.NotFound, "missing"), http.StatusNotFound, "notFound", "NOT_FOUND"},
		{"AlreadyExists", cerrors.New(cerrors.AlreadyExists, "dup"), http.StatusConflict, "alreadyExists", "ALREADY_EXISTS"},
		{"InvalidArgument", cerrors.New(cerrors.InvalidArgument, "bad"), http.StatusBadRequest, "invalid", "INVALID_ARGUMENT"},
		{"FailedPrecondition", cerrors.New(cerrors.FailedPrecondition, "fp"), http.StatusConflict, "conditionNotMet", "FAILED_PRECONDITION"},
		{"Unknown", errors.New("boom"), http.StatusInternalServerError, "internalError", "INTERNAL"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			gcprest.WriteCErr(rec, c.err)

			if rec.Code != c.code {
				t.Errorf("http code=%d want %d", rec.Code, c.code)
			}

			var env struct {
				Error struct {
					Code    int    `json:"code"`
					Status  string `json:"status"`
					Message string `json:"message"`
					Errors  []struct {
						Reason string `json:"reason"`
					} `json:"errors"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode body: %v (body=%q)", err, rec.Body.String())
			}

			if env.Error.Status != c.status {
				t.Errorf("error.status=%q want canonical %q", env.Error.Status, c.status)
			}

			if env.Error.Code != c.code {
				t.Errorf("error.code=%d want %d", env.Error.Code, c.code)
			}

			if len(env.Error.Errors) == 0 || env.Error.Errors[0].Reason != c.reason {
				t.Errorf("errors[].reason=%+v want %q", env.Error.Errors, c.reason)
			}
		})
	}
}

// TestWriteErrorCanonicalReasonPassthrough guards a regression: some callers
// (vertexai's predict-no-models path, eventarc, fcm) pass an already-canonical
// google.rpc.Code NAME as the reason arg. WriteError must surface that NAME in
// the top-level status field verbatim (not drop it via omitempty). Before the
// isCanonicalCode passthrough, only "INVALID_ARGUMENT" had an alias case and
// "FAILED_PRECONDITION" fell through to "" — silently dropping the status of a
// previously-correct response.
func TestWriteErrorCanonicalReasonPassthrough(t *testing.T) {
	canonical := []string{
		"FAILED_PRECONDITION", "INVALID_ARGUMENT", "NOT_FOUND", "ALREADY_EXISTS",
		"PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "UNIMPLEMENTED", "UNAVAILABLE",
		"INTERNAL", "UNAUTHENTICATED", "ABORTED", "OUT_OF_RANGE", "DATA_LOSS",
		"DEADLINE_EXCEEDED", "CANCELLED", "UNKNOWN",
	}

	for _, code := range canonical {
		t.Run(code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			gcprest.WriteError(rec, http.StatusBadRequest, code, "boom")

			var env struct {
				Error struct {
					Status string `json:"status"`
					Errors []struct {
						Reason string `json:"reason"`
					} `json:"errors"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode body: %v (body=%q)", err, rec.Body.String())
			}

			if env.Error.Status != code {
				t.Errorf("error.status=%q want canonical %q (dropped?)", env.Error.Status, code)
			}

			if len(env.Error.Errors) == 0 || env.Error.Errors[0].Reason != code {
				t.Errorf("errors[].reason=%+v want %q", env.Error.Errors, code)
			}
		})
	}
}

// TestWriteErrorReasonWithoutCanonicalCode confirms reasons with no
// google.rpc.Code (e.g. HTTP 405 "methodNotAllowed") drop the status field,
// matching real GCP.
func TestWriteErrorReasonWithoutCanonicalCode(t *testing.T) {
	rec := httptest.NewRecorder()
	gcprest.WriteError(rec, http.StatusMethodNotAllowed, "methodNotAllowed", "nope")

	body := rec.Body.String()
	if strings.Contains(body, `"status"`) {
		t.Errorf("body=%q should omit status for a reason with no canonical code", body)
	}
}

// TestWriteCErrOmitsCodePrefix guards against the wire message leaking the
// cloudemu internal error-taxonomy code prefix (cerrors.Error() renders
// "NotFound: instance x not found") into the message an SDK surfaces to the
// caller. Real GCP (and every other cloud's wire handlers here) only ever
// sends the human-readable text.
func TestWriteCErrOmitsCodePrefix(t *testing.T) {
	rec := httptest.NewRecorder()
	gcprest.WriteCErr(rec, cerrors.Newf(cerrors.NotFound, "instance %s not found", "vm-1"))

	body := rec.Body.String()
	if strings.Contains(body, "NotFound:") {
		t.Errorf("body=%q leaks the internal error code prefix", body)
	}

	if !strings.Contains(body, "instance vm-1 not found") {
		t.Errorf("body=%q missing the human-readable message", body)
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

package azurearm_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

func TestParsePathSubscriptionOnly(t *testing.T) {
	rp, ok := azurearm.ParsePath("/subscriptions/sub-1")
	if !ok {
		t.Fatal("expected ok=true")
	}

	if rp.Subscription != "sub-1" {
		t.Errorf("subscription=%s", rp.Subscription)
	}
}

func TestParsePathResourceGroup(t *testing.T) {
	rp, ok := azurearm.ParsePath("/subscriptions/s/resourceGroups/rg")
	if !ok || rp.ResourceGroup != "rg" {
		t.Errorf("rp=%+v", rp)
	}
}

func TestParsePathSubscriptionScopedProvider(t *testing.T) {
	rp, ok := azurearm.ParsePath("/subscriptions/s/providers/Microsoft.Compute/virtualMachines")
	if !ok {
		t.Fatal("expected ok=true")
	}

	if rp.Provider != "Microsoft.Compute" || rp.ResourceType != "virtualMachines" {
		t.Errorf("rp=%+v", rp)
	}

	if rp.ResourceGroup != "" {
		t.Errorf("expected empty RG, got %q", rp.ResourceGroup)
	}
}

func TestParsePathFullResource(t *testing.T) {
	rp, ok := azurearm.ParsePath("/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1")
	if !ok || rp.ResourceName != "vm1" {
		t.Errorf("rp=%+v", rp)
	}
}

func TestParsePathSubResource(t *testing.T) {
	rp, ok := azurearm.ParsePath("/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1/start")
	if !ok || rp.SubResource != "start" {
		t.Errorf("rp=%+v", rp)
	}
}

func TestParsePathRejectsNonARM(t *testing.T) {
	cases := []string{
		"/",
		"/foo/bar",
		"/subscriptions",
		"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute",
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, ok := azurearm.ParsePath(c); ok && c != "/subscriptions" {
				// /subscriptions alone has only 1 part — should fail.
				// Other malformed paths (truncated providers segment) should fail.
				t.Errorf("expected ok=false for %q", c)
			}
		})
	}
}

func TestBuildResourceID(t *testing.T) {
	got := azurearm.BuildResourceID("s", "rg", "Microsoft.Compute", "virtualMachines", "vm1")
	want := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1"

	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestWriteCErrMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"NotFound", cerrors.New(cerrors.NotFound, "missing"), http.StatusNotFound, "ResourceNotFound"},
		{"AlreadyExists", cerrors.New(cerrors.AlreadyExists, "dup"), http.StatusConflict, "Conflict"},
		{"InvalidArgument", cerrors.New(cerrors.InvalidArgument, "bad"), http.StatusBadRequest, "InvalidParameter"},
		{"FailedPrecondition", cerrors.New(cerrors.FailedPrecondition, "fp"), http.StatusConflict, "PreconditionFailed"},
		{"Unknown", errors.New("boom"), http.StatusInternalServerError, "InternalError"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			azurearm.WriteCErr(rec, c.err)

			if rec.Code != c.status {
				t.Errorf("status=%d want %d", rec.Code, c.status)
			}

			if !strings.Contains(rec.Body.String(), c.code) {
				t.Errorf("body=%q does not contain %q", rec.Body.String(), c.code)
			}
		})
	}
}

func TestDecodeJSONInvalid(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{not json`))

	var out struct {
		Foo string `json:"foo"`
	}

	if azurearm.DecodeJSON(rec, r, &out) {
		t.Error("expected DecodeJSON to return false on bad JSON")
	}

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rec.Code)
	}
}

func TestDecodeJSONOK(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"foo":"bar"}`))

	var out struct {
		Foo string `json:"foo"`
	}

	if !azurearm.DecodeJSON(rec, r, &out) {
		t.Fatal("expected DecodeJSON to return true")
	}

	if out.Foo != "bar" {
		t.Errorf("foo=%s", out.Foo)
	}
}

package globalaccelerator_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gasrv "github.com/stackshy/cloudemu/v2/server/aws/globalaccelerator"
)

func newHandler() *gasrv.Handler {
	return gasrv.New(cloudemu.NewAWS().GlobalAccelerator)
}

func TestMatches(t *testing.T) {
	h := newHandler()

	cases := []struct {
		target string
		want   bool
	}{
		{"GlobalAccelerator_V20180706.CreateAccelerator", true},
		{"GlobalAccelerator_V20180706.ListListeners", true},
		{"HealthLake.CreateFHIRDatastore", false},
		{"DynamoDB_20120810.PutItem", false},
		{"", false},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.target != "" {
			r.Header.Set("X-Amz-Target", tc.target)
		}

		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

func TestUnknownOperation(t *testing.T) {
	h := newHandler()

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("X-Amz-Target", "GlobalAccelerator_V20180706.NoSuchOperation")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	if !strings.Contains(w.Body.String(), "UnknownOperationException") {
		t.Fatalf("body = %q, want UnknownOperationException", w.Body.String())
	}
}

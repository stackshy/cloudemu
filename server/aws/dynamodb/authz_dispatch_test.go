package dynamodb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
)

// TestIAMOperationsAreServed fails when the IAM table names an operation the
// handler does not serve, so the table and dispatch agree.
func TestIAMOperationsAreServed(t *testing.T) {
	h := New(cloudemu.NewAWS().DynamoDB)

	for op := range tableOps {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		r.Header.Set("X-Amz-Target", targetPrefix+op)

		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if strings.Contains(w.Body.String(), "UnknownOperationException") {
			t.Errorf("%s is in the IAM table but the handler does not serve it", op)
		}
	}
}

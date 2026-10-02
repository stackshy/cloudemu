package wire

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestWriteJSONErrorSetsErrortypeHeader guards that the shared awsJson error
// writers stamp the X-Amzn-Errortype header (not just the __type body). botocore
// and the AWS CLI read this header to resolve the modeled exception type, so the
// awsJson1.x services that use these helpers (DynamoDB, SQS, Step Functions,
// etc.) must emit it or those clients cannot recognize the error.
func TestWriteJSONErrorSetsErrortypeHeader(t *testing.T) {
	const errType = "ResourceNotFoundException"

	t.Run("WriteJSONError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteJSONError(rec, 400, errType, "no such thing")

		if got := rec.Header().Get("X-Amzn-Errortype"); got != errType {
			t.Fatalf("X-Amzn-Errortype = %q, want %q", got, errType)
		}

		assertTypeInBody(t, rec.Body.Bytes(), errType)
	})

	t.Run("WriteJSONErrorQueryCompat", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteJSONErrorQueryCompat(rec, 400, errType, "QueueDoesNotExist;Sender", "gone")

		if got := rec.Header().Get("X-Amzn-Errortype"); got != errType {
			t.Fatalf("X-Amzn-Errortype = %q, want %q", got, errType)
		}
		// The pre-existing query-compat header must still be present.
		if got := rec.Header().Get("x-amzn-query-error"); got != "QueueDoesNotExist;Sender" {
			t.Fatalf("x-amzn-query-error = %q, want %q", got, "QueueDoesNotExist;Sender")
		}
	})

	t.Run("WriteJSONErrorFields", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteJSONErrorFields(rec, 400, errType, "conflict", map[string]any{"Item": map[string]any{}})

		if got := rec.Header().Get("X-Amzn-Errortype"); got != errType {
			t.Fatalf("X-Amzn-Errortype = %q, want %q", got, errType)
		}
	})
}

func assertTypeInBody(t *testing.T, body []byte, want string) {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}

	if decoded["__type"] != want {
		t.Fatalf("body __type = %v, want %q", decoded["__type"], want)
	}
}

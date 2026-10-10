package ecs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// namedError is a driver error that carries a precise ECS exception name.
type namedError struct {
	err       *cerrors.Error
	exception string
}

func (e *namedError) Error() string        { return e.err.Error() }
func (e *namedError) ECSException() string { return e.exception }
func (e *namedError) Unwrap() error        { return e.err }

func TestWriteErr_NewExceptions(t *testing.T) {
	for _, name := range []string{
		"TaskSetNotFoundException", "ServiceNotActiveException", "ServiceDeploymentNotFoundException",
		"ConflictException", "UnsupportedFeatureException", "InvalidParameterException", "ClusterNotFoundException",
	} {
		rec := httptest.NewRecorder()
		writeErr(rec, &namedError{err: cerrors.New(cerrors.NotFound, "boom"), exception: name})

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}

		if !strings.Contains(rec.Body.String(), `"`+name+`"`) {
			t.Fatalf("%s: body %s", name, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	writeErr(rec, &namedError{err: cerrors.New(cerrors.Internal, "boom"), exception: "ServerException"})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ServerException: status %d, want 500", rec.Code)
	}
}

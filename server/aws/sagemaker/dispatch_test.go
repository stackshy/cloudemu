package sagemaker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	smmock "github.com/stackshy/cloudemu/v2/providers/aws/sagemaker"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

func newHandler() *Handler {
	return New(smmock.New(config.NewOptions()))
}

func TestMatches(t *testing.T) {
	h := newHandler()

	cases := []struct {
		name   string
		method string
		path   string
		target string
		want   bool
	}{
		{"control plane target", http.MethodPost, "/", "SageMaker.ListModels", true},
		{"runtime invoke", http.MethodPost, "/endpoints/e/invocations", "", true},
		{"runtime async", http.MethodPost, "/endpoints/e/async-invocations", "", true},
		{"feature store", http.MethodGet, "/FeatureGroup/g", "", true},
		{"other service target", http.MethodPost, "/", "AmazonSSM.GetParameter", false},
		{"plain s3 path", http.MethodGet, "/bucket/key", "", false},
		{"endpoints list is not runtime", http.MethodGet, "/endpoints/e", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.target != "" {
				r.Header.Set("X-Amz-Target", tc.target)
			}

			assert.Equal(t, tc.want, h.Matches(r))
		})
	}
}

func TestUnknownOperation(t *testing.T) { // T9
	h := newHandler()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("X-Amz-Target", "SageMaker.NoSuchOperation")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "UnknownOperationException", w.Header().Get("X-Amzn-Errortype"))
}

func TestErrorMapping(t *testing.T) { // T10
	cases := []struct {
		name string
		err  error
		code int
		typ  string
	}{
		{"not found", cerrors.New(cerrors.NotFound, "x"), 400, "ResourceNotFound"},
		// A plain already-exists is the validation-style duplicate (CreateModel, CreateEndpoint, ...); operations whose SDK
		// error list has ResourceInUse return driver.InUse from the provider, covered per op in the provider and SDK tests.
		{"plain already exists (no ResourceInUse on the op)", cerrors.New(cerrors.AlreadyExists, "x"), 400, "ValidationException"},
		{"invalid argument", cerrors.New(cerrors.InvalidArgument, "x"), 400, "ValidationException"},
		{"failed precondition", cerrors.New(cerrors.FailedPrecondition, "x"), 400, "ValidationException"},
		{"exhausted is limit exceeded", cerrors.New(cerrors.ResourceExhausted, "x"), 400, "ResourceLimitExceeded"},
		{"conflict", driver.Conflict("x"), 400, "ConflictException"},
		{"in use", driver.InUse("x"), 400, "ResourceInUse"},
		{"limit", driver.LimitExceeded("x"), 400, "ResourceLimitExceeded"},
		{"internal", cerrors.New(cerrors.Internal, "x"), 500, "InternalFailure"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeDriverError(w, tc.err)

			require.Equal(t, tc.code, w.Code)
			assert.Equal(t, tc.typ, w.Header().Get("X-Amzn-Errortype"))
		})
	}
}

// restServed are driver methods served over REST paths (feature-store online
// runtime), not by an X-Amz-Target operation.
var restServed = map[string]bool{"PutRecord": true, "GetRecord": true, "DeleteRecord": true} //nolint:gochecknoglobals // test table

// kindParameterised driver methods serve one route per monitoring kind
// (CreateDataQualityJobDefinition, CreateModelBiasJobDefinition, ...).
var kindParameterised = map[string]string{ //nolint:gochecknoglobals // test table
	"CreateMonitoringJobDefinition":   "Create%sJobDefinition",
	"DescribeMonitoringJobDefinition": "Describe%sJobDefinition",
	"ListMonitoringJobDefinitions":    "List%sJobDefinitions",
	"DeleteMonitoringJobDefinition":   "Delete%sJobDefinition",
}

func TestOpMapCompleteness(t *testing.T) { // T8
	h := newHandler()
	typ := reflect.TypeOf((*driver.SageMaker)(nil)).Elem()
	served := map[string]bool{}

	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if restServed[name] {
			continue
		}

		if format, ok := kindParameterised[name]; ok {
			for _, kind := range driver.MonitoringKinds {
				op := fmt.Sprintf(format, kind)
				_, routed := h.ops[op]
				assert.True(t, routed, "driver method %s has no route %s", name, op)

				served[op] = true
			}

			continue
		}

		_, routed := h.ops[name]
		assert.True(t, routed, "driver method %s has no route in routes()", name)

		served[name] = true
	}

	for op := range h.ops {
		_, ok := typ.MethodByName(op)
		assert.True(t, ok || served[op] || op == "AddTags", "route %s has no driver method", op)
	}
}

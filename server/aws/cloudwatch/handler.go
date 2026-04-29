// Package cloudwatch implements AWS CloudWatch's Smithy RPC-v2-CBOR protocol
// as a server.Handler.
//
// Modern aws-sdk-go-v2 CloudWatch clients no longer use the AWS query protocol
// — they send CBOR-encoded request bodies to URLs like
// /service/GraniteServiceVersion20100801/operation/<Operation>, with headers:
//
//	Smithy-Protocol: rpc-v2-cbor
//	Content-Type:    application/cbor
//
// This handler matches those requests, decodes CBOR, dispatches to the
// monitoring driver, and writes CBOR responses.
package cloudwatch

import (
	"io"
	"net/http"
	"strings"

	"github.com/fxamacker/cbor/v2"

	cerrors "github.com/stackshy/cloudemu/errors"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

const (
	protocolHeader  = "Smithy-Protocol"
	protocolValue   = "rpc-v2-cbor"
	cborContentType = "application/cbor"
	pathPrefix      = "/service/"
	opMarker        = "/operation/"
	maxBodyBytes    = 1 << 20
)

// Handler serves CloudWatch rpc-v2-cbor requests against a monitoring driver.
type Handler struct {
	monitoring mondriver.Monitoring
}

// New returns a CloudWatch handler backed by m.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{monitoring: m}
}

// Matches returns true for Smithy rpc-v2-cbor requests.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get(protocolHeader) != protocolValue {
		return false
	}

	return strings.HasPrefix(r.URL.Path, pathPrefix)
}

// ServeHTTP parses the URL path for the operation name and dispatches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := extractOperation(r.URL.Path)
	if op == "" {
		writeCBORError(w, http.StatusBadRequest, "InvalidRequest", "missing operation in path")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeCBORError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}

	switch op {
	case "PutMetricData":
		h.putMetricData(w, r, body)
	case "GetMetricStatistics":
		h.getMetricStatistics(w, r, body)
	case "ListMetrics":
		h.listMetrics(w, r, body)
	case "PutMetricAlarm":
		h.putMetricAlarm(w, r, body)
	case "DescribeAlarms":
		h.describeAlarms(w, r, body)
	case "DeleteAlarms":
		h.deleteAlarms(w, r, body)
	default:
		writeCBORError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown operation: "+op)
	}
}

// extractOperation pulls the <Op> out of /service/<svc>/operation/<Op>.
func extractOperation(path string) string {
	i := strings.Index(path, opMarker)
	if i < 0 {
		return ""
	}

	return path[i+len(opMarker):]
}

// writeCBORError writes an rpc-v2-cbor error response.
func writeCBORError(w http.ResponseWriter, status int, errType, msg string) {
	payload := map[string]any{
		"__type":  errType,
		"Message": msg,
	}

	body, _ := cbor.Marshal(payload)

	w.Header().Set(protocolHeader, protocolValue)
	w.Header().Set("Content-Type", cborContentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// smithyEncMode configures CBOR encoding to match AWS Smithy rpc-v2-cbor:
// timestamps are emitted with tag 1 (epoch seconds as float64), which is what
// aws-sdk-go-v2 decoders expect.
var smithyEncMode = mustSmithyEncMode() //nolint:gochecknoglobals // reused encoder

func mustSmithyEncMode() cbor.EncMode {
	mode, err := cbor.EncOptions{Time: cbor.TimeUnixDynamic, TimeTag: cbor.EncTagRequired}.EncMode()
	if err != nil {
		panic(err)
	}

	return mode
}

// writeCBORResponse writes a successful rpc-v2-cbor response body.
func writeCBORResponse(w http.ResponseWriter, payload any) {
	body, err := smithyEncMode.Marshal(payload)
	if err != nil {
		writeCBORError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set(protocolHeader, protocolValue)
	w.Header().Set("Content-Type", cborContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeDriverErr maps CloudEmu errors to CloudWatch error responses.
func writeDriverErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeCBORError(w, http.StatusBadRequest, "ResourceNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeCBORError(w, http.StatusBadRequest, "ResourceAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeCBORError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	default:
		writeCBORError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

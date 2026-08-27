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

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	protocolHeader  = "Smithy-Protocol"
	protocolValue   = "rpc-v2-cbor"
	cborContentType = "application/cbor"
	pathPrefix      = "/service/"
	opMarker        = "/operation/"
	maxBodyBytes    = 1 << 20
)

// Operation names shared by the rpc-v2-cbor and query dispatch switches.
const (
	opPutMetricData       = "PutMetricData"
	opGetMetricStatistics = "GetMetricStatistics"
	opListMetrics         = "ListMetrics"
	opPutMetricAlarm      = "PutMetricAlarm"
	opDescribeAlarms      = "DescribeAlarms"
	opDeleteAlarms        = "DeleteAlarms"
	opSetAlarmState       = "SetAlarmState"
	opPutCompositeAlarm   = "PutCompositeAlarm"
	opPutDashboard        = "PutDashboard"
	opGetDashboard        = "GetDashboard"
	opListDashboards      = "ListDashboards"
	opDeleteDashboards    = "DeleteDashboards"
)

// Handler serves CloudWatch rpc-v2-cbor requests against a monitoring driver.
// An optional IPAM metrics source lets the handler surface derived AWS/IPAM
// metrics that the monitoring store itself doesn't hold.
type Handler struct {
	monitoring mondriver.Monitoring
	ipam       netdriver.IPAMMetrics
}

// New returns a CloudWatch handler backed by m. Use SetIPAMMetrics to attach
// the optional derived AWS/IPAM metrics source. Kept single-argument so callers
// that don't wire IPAM (e.g. the base query-protocol tests) construct it
// unchanged.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{monitoring: m}
}

// SetIPAMMetrics attaches an optional IPAMMetrics source (nil-safe) supplying
// the derived AWS/IPAM namespace metrics, following the same setter-injection
// pattern as the other CloudEmu handlers.
func (h *Handler) SetIPAMMetrics(ipam netdriver.IPAMMetrics) {
	h.ipam = ipam
}

// Matches returns true for Smithy rpc-v2-cbor requests, and for classic
// query-protocol CloudWatch requests (used by the AWS CLI and older SDKs),
// disambiguated from EC2 by the SigV4 "monitoring" credential scope.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get(protocolHeader) == protocolValue && strings.HasPrefix(r.URL.Path, pathPrefix) {
		return true
	}

	return isQueryRequest(r)
}

// ServeHTTP parses the URL path for the operation name and dispatches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isQueryRequest(r) {
		h.serveQuery(w, r)
		return
	}

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

	h.dispatch(w, r, op, body)
}

// dispatch routes a decoded rpc-v2-cbor operation to its handler.
//
//nolint:gocyclo // first-match dispatch over many CloudWatch operations.
func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request, op string, body []byte) {
	switch op {
	case opPutMetricData:
		h.putMetricData(w, r, body)
	case opGetMetricStatistics:
		h.getMetricStatistics(w, r, body)
	case "GetMetricData":
		h.getMetricData(w, r, body)
	case opListMetrics:
		h.listMetrics(w, r, body)
	case opPutMetricAlarm:
		h.putMetricAlarm(w, r, body)
	case opDescribeAlarms:
		h.describeAlarms(w, r, body)
	case "DescribeAlarmsForMetric":
		h.describeAlarmsForMetric(w, r, body)
	case "DescribeAlarmHistory":
		h.describeAlarmHistory(w, r, body)
	case opDeleteAlarms:
		h.deleteAlarms(w, r, body)
	case opSetAlarmState:
		h.setAlarmState(w, r, body)
	case opPutCompositeAlarm:
		h.putCompositeAlarm(w, r, body)
	case opPutDashboard:
		h.putDashboard(w, r, body)
	case opGetDashboard:
		h.getDashboard(w, r, body)
	case opListDashboards:
		h.listDashboards(w, r, body)
	case opDeleteDashboards:
		h.deleteDashboards(w, r, body)
	case "EnableAlarmActions":
		h.setAlarmActionsEnabled(w, r, body, true)
	case "DisableAlarmActions":
		h.setAlarmActionsEnabled(w, r, body, false)
	case "TagResource":
		h.tagResource(w, r, body)
	case "UntagResource":
		h.untagResource(w, r, body)
	case "ListTagsForResource":
		h.listTagsForResource(w, r, body)
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
		"message": msg,
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

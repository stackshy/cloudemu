// Package cloudwatch implements AWS CloudWatch as a server.Handler over the
// three protocols its clients speak: Smithy RPC-v2-CBOR, awsJson1_0 and query.
//
// Modern aws-sdk-go-v2 CloudWatch clients no longer use the AWS query protocol.
// They send CBOR-encoded request bodies to URLs like
// /service/GraniteServiceVersion20100801/operation/<Operation>, with headers:
//
//	Smithy-Protocol: rpc-v2-cbor
//	Content-Type:    application/cbor
//
// This handler matches those requests, decodes CBOR, dispatches to the
// monitoring driver, and writes CBOR responses. botocore 1.43+ sends
// awsJson1_0 instead (json_protocol.go), and the AWS CLI v2 and older SDKs
// send query (query.go). All three run through the same per-op cores.
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
	opPutMetricData            = "PutMetricData"
	opGetMetricStatistics      = "GetMetricStatistics"
	opListMetrics              = "ListMetrics"
	opPutMetricAlarm           = "PutMetricAlarm"
	opDescribeAlarms           = "DescribeAlarms"
	opDescribeAlarmHistory     = "DescribeAlarmHistory"
	opDeleteAlarms             = "DeleteAlarms"
	opSetAlarmState            = "SetAlarmState"
	opPutCompositeAlarm        = "PutCompositeAlarm"
	opPutDashboard             = "PutDashboard"
	opGetDashboard             = "GetDashboard"
	opListDashboards           = "ListDashboards"
	opDeleteDashboards         = "DeleteDashboards"
	opPutMetricStream          = "PutMetricStream"
	opGetMetricStream          = "GetMetricStream"
	opListMetricStreams        = "ListMetricStreams"
	opDeleteMetricStream       = "DeleteMetricStream"
	opStartMetricStreams       = "StartMetricStreams"
	opStopMetricStreams        = "StopMetricStreams"
	opTagResource              = "TagResource"
	opUntagResource            = "UntagResource"
	opListTagsForResource      = "ListTagsForResource"
	opEnableAlarmActions       = "EnableAlarmActions"
	opDisableAlarmActions      = "DisableAlarmActions"
	opGetMetricData            = "GetMetricData"
	opDescribeAlarmsForMetric  = "DescribeAlarmsForMetric"
	opPutAnomalyDetector       = "PutAnomalyDetector"
	opDescribeAnomalyDetectors = "DescribeAnomalyDetectors"
	opDeleteAnomalyDetector    = "DeleteAnomalyDetector"
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

// Matches returns true for Smithy rpc-v2-cbor requests, for awsJson1_0
// requests whose X-Amz-Target names the CloudWatch service, and for classic
// query-protocol CloudWatch requests (used by the AWS CLI and older SDKs),
// disambiguated from EC2 by the SigV4 "monitoring" credential scope.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get(protocolHeader) == protocolValue && strings.HasPrefix(r.URL.Path, pathPrefix) {
		return true
	}

	return isJSONRequest(r) || isQueryRequest(r)
}

// ServeHTTP parses the URL path for the operation name and dispatches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	decodeRequestBody(r)

	if r.Header.Get(protocolHeader) != protocolValue && isJSONRequest(r) {
		h.serveJSON(w, r)
		return
	}

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
	case opGetMetricData:
		h.getMetricData(w, r, body)
	case opListMetrics:
		h.listMetrics(w, r, body)
	case opPutMetricAlarm:
		h.putMetricAlarm(w, r, body)
	case opDescribeAlarms:
		h.describeAlarms(w, r, body)
	case opDescribeAlarmsForMetric:
		h.describeAlarmsForMetric(w, r, body)
	case opDescribeAlarmHistory:
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
	case opPutMetricStream:
		h.putMetricStream(w, r, body)
	case opGetMetricStream:
		h.getMetricStream(w, r, body)
	case opListMetricStreams:
		h.listMetricStreams(w, r, body)
	case opDeleteMetricStream:
		h.deleteMetricStream(w, r, body)
	case opStartMetricStreams:
		h.startMetricStreams(w, r, body)
	case opStopMetricStreams:
		h.stopMetricStreams(w, r, body)
	case opEnableAlarmActions:
		h.setAlarmActionsEnabled(w, r, body, true)
	case opDisableAlarmActions:
		h.setAlarmActionsEnabled(w, r, body, false)
	case opTagResource:
		h.tagResource(w, r, body)
	case opUntagResource:
		h.untagResource(w, r, body)
	case opListTagsForResource:
		h.listTagsForResource(w, r, body)
	case opPutAnomalyDetector:
		h.putAnomalyDetector(w, r, body)
	case opDescribeAnomalyDetectors:
		h.describeAnomalyDetectors(w, r, body)
	case opDeleteAnomalyDetector:
		h.deleteAnomalyDetector(w, r, body)
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

// writeCBORError writes an rpc-v2-cbor error response, or the awsJson1_0
// error when w is the writer of a JSON request.
func writeCBORError(w http.ResponseWriter, status int, errType, msg string) {
	if jw, ok := w.(*jsonWriter); ok {
		jw.writeError(status, errType, msg)
		return
	}

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

// writeCBORResponse writes a successful rpc-v2-cbor response body, or the
// awsJson1_0 body when w is the writer of a JSON request.
func writeCBORResponse(w http.ResponseWriter, payload any) {
	if jw, ok := w.(*jsonWriter); ok {
		jw.writeResult(payload)
		return
	}

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
	if we, ok := asWireError(err); ok {
		writeCBORError(w, we.status, we.code, we.msg)
		return
	}

	switch {
	case cerrors.IsNotFound(err):
		// ResourceNotFound is a 404 in the CloudWatch API model.
		writeCBORError(w, http.StatusNotFound, "ResourceNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeCBORError(w, http.StatusBadRequest, "ResourceAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeCBORError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	default:
		writeCBORError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

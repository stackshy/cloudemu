// Package ec2 implements the AWS EC2 query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 EC2 client at a Server registered with this
// handler and operations work against an in-memory compute driver.
package ec2

import (
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// formContentType is the request Content-Type AWS SDKs use for the query
// protocol (form-encoded POST). We match this prefix rather than a strict
// equality because SDKs append "; charset=utf-8".
const formContentType = "application/x-www-form-urlencoded"

// maxFormBodyBytes caps EC2 form-encoded request bodies. Real EC2 requests
// are small (a few KB even for deeply nested TagSpecifications), so 1 MiB is
// plenty of headroom while preventing memory-exhaustion attacks.
const maxFormBodyBytes = 1 << 20

// Handler serves EC2 query-protocol requests against a compute driver.
type Handler struct {
	compute computedriver.Compute
}

// New returns an EC2 handler backed by c.
func New(c computedriver.Compute) *Handler {
	return &Handler{compute: c}
}

// Matches returns true for EC2-shaped requests. EC2 uses the AWS query
// protocol: either a POST with form-encoded body (the SDK default) or a GET
// with ?Action=... on the URL. It never sets X-Amz-Target; that's reserved
// for JSON-RPC services like DynamoDB.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.URL.Query().Get("Action") != "" {
		return true
	}

	if r.Method == http.MethodPost &&
		strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return true
	}

	return false
}

// ServeHTTP parses the request form and dispatches on Action.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)

	if err := r.ParseForm(); err != nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}

	action := r.Form.Get("Action")

	switch action {
	case "RunInstances":
		h.runInstances(w, r)
	case "DescribeInstances":
		h.describeInstances(w, r)
	case "StartInstances":
		h.startInstances(w, r)
	case "StopInstances":
		h.stopInstances(w, r)
	case "RebootInstances":
		h.rebootInstances(w, r)
	case "TerminateInstances":
		h.terminateInstances(w, r)
	case "ModifyInstanceAttribute":
		h.modifyInstanceAttribute(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown action: "+action)
	}
}

// writeErr maps CloudEmu errors to EC2 XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidInstanceID.NotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"ResourceAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidParameterValue", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"IncorrectInstanceState", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError,
			"InternalError", err.Error())
	}
}

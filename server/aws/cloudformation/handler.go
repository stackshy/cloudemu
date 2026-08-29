// Package cloudformation implements the AWS CloudFormation query protocol as a
// server.Handler. Point the real aws-sdk-go-v2 CloudFormation client (or a SAM /
// CDK / CFN deploy) at a Server registered with this handler and stack
// operations run against the in-memory orchestrator, provisioning each resource
// through the backing AWS service drivers.
//
// CloudFormation shares the AWS query wire shape (POST + form-encoded body, XML
// response) with EC2, RDS, IAM, SNS, and ElastiCache. To keep dispatch
// unambiguous, Matches parses the form once and claims only requests whose
// Action is one of the known CloudFormation operations. The EC2 handler is the
// catch-all for other query-protocol actions, so this handler MUST register
// before EC2.
//
// Coverage (query protocol):
//
//	CreateStack              — API.CreateStack
//	UpdateStack              — API.UpdateStack
//	DeleteStack              — API.DeleteStack
//	DescribeStacks           — API.DescribeStacks
//	DescribeStackEvents      — API.DescribeStackEvents
//	ListStacks               — API.ListStacks
//	DescribeStackResources   — API.DescribeStackResources
//	ListStackResources       — API.ListStackResources
//	GetTemplate              — API.GetTemplate
package cloudformation

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Namespace is the XML namespace for CloudFormation query responses.
const Namespace = "http://cloudformation.amazonaws.com/doc/2010-05-15/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 8 << 20 // templates can be large
)

// cfnActions is the set of Action values this handler recognizes.
var cfnActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateStack":            {},
	"UpdateStack":            {},
	"DeleteStack":            {},
	"DescribeStacks":         {},
	"DescribeStackEvents":    {},
	"ListStacks":             {},
	"DescribeStackResources": {},
	"ListStackResources":     {},
	"GetTemplate":            {},
}

// Handler serves CloudFormation query-protocol requests against a stack API.
type Handler struct {
	api cfn.API
}

// New returns a CloudFormation handler backed by api.
func New(api cfn.API) *Handler {
	return &Handler{api: api}
}

// Matches reports whether the request is an AWS CloudFormation query-protocol
// call (POST + form-encoded body whose Action is one of the known operations).
// Parsing the form here caches it on the request for ServeHTTP.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return false
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		return false
	}

	_, ok := cfnActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form was parsed by Matches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case "CreateStack":
		h.createStack(w, r)
	case "UpdateStack":
		h.updateStack(w, r)
	case "DeleteStack":
		h.deleteStack(w, r)
	case "DescribeStacks":
		h.describeStacks(w, r)
	case "DescribeStackEvents":
		h.describeStackEvents(w, r)
	case "ListStacks":
		h.listStacks(w, r)
	case "DescribeStackResources":
		h.describeStackResources(w, r)
	case "ListStackResources":
		h.listStackResources(w, r)
	case "GetTemplate":
		h.getTemplate(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
			"unknown CloudFormation action: "+r.Form.Get("Action"))
	}
}

// writeErr maps cloudemu errors to CloudFormation XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "ValidationError", msg)
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "AlreadyExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "ValidationError", msg)
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", msg)
	}
}

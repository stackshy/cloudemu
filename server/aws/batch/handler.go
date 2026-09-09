// Package batch implements the AWS Batch control-plane API (REST-JSON,
// awsRestjson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/batch client (or the `aws batch` CLI, or the
// aws_batch_* Terraform resources) at a Server registered with this handler and
// the compute-environment, job-queue, and job-definition operations work
// end-to-end against an in-memory driver.
//
// Batch routes by HTTP verb + a fixed operation path under the /v1/ prefix
// (e.g. POST /v1/createcomputeenvironment); there is no X-Amz-Target header.
// Matches claims only /v1/ paths whose first segment is a known Batch operation,
// so it never shadows the S3 catch-all and is disjoint from the MSK handler
// (which also uses /v1/ but with different collection roots).
package batch

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// apiPrefix is the version path prefix every Batch operation lives under.
const apiPrefix = "/v1/"

// Operation path segments (all lowercase, matching the Batch REST bindings).
const (
	opCreateComputeEnvironment    = "createcomputeenvironment"
	opDescribeComputeEnvironments = "describecomputeenvironments"
	opUpdateComputeEnvironment    = "updatecomputeenvironment"
	opDeleteComputeEnvironment    = "deletecomputeenvironment"
	opCreateJobQueue              = "createjobqueue"
	opDescribeJobQueues           = "describejobqueues"
	opUpdateJobQueue              = "updatejobqueue"
	opDeleteJobQueue              = "deletejobqueue"
	opRegisterJobDefinition       = "registerjobdefinition"
	opDescribeJobDefinitions      = "describejobdefinitions"
	opDeregisterJobDefinition     = "deregisterjobdefinition"
	opTags                        = "tags"
)

// Handler serves AWS Batch requests against a driver.
type Handler struct {
	batch driver.Batch
}

// New returns a Batch handler backed by d.
func New(d driver.Batch) *Handler {
	return &Handler{batch: d}
}

// knownOps is the set of first-path segments this handler claims.
//
//nolint:gochecknoglobals // immutable routing set, read-only after init
var knownOps = map[string]bool{
	opCreateComputeEnvironment:    true,
	opDescribeComputeEnvironments: true,
	opUpdateComputeEnvironment:    true,
	opDeleteComputeEnvironment:    true,
	opCreateJobQueue:              true,
	opDescribeJobQueues:           true,
	opUpdateJobQueue:              true,
	opDeleteJobQueue:              true,
	opRegisterJobDefinition:       true,
	opDescribeJobDefinitions:      true,
	opDeregisterJobDefinition:     true,
	opTags:                        true,
}

// Matches claims requests under /v1/ whose first segment is a known Batch
// operation. The operation-name segments are disjoint from every other /v1/
// handler, so this never shadows them or the S3 catch-all.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		return false
	}

	// The /v1/tags/{arn} path is shared with other restJson1 services (notably
	// MSK, which registers after us). Claim it only for Batch ARNs so a kafka
	// (or other) ARN falls through to its owning handler.
	if segs[0] == opTags {
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), ":batch:")
	}

	return knownOps[segs[0]]
}

// ServeHTTP dispatches a Batch request on its operation-path segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(r.URL.Path, apiPrefix))
	if len(segs) == 0 {
		writeError(w, http.StatusBadRequest, exceptionClient, "unsupported path: "+r.URL.Path)

		return
	}

	if segs[0] == opTags {
		h.serveTags(w, r)

		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusBadRequest, exceptionClient, "method not allowed: "+r.Method)

		return
	}

	h.serveOperation(w, r, segs[0])
}

// serveOperation routes a POST operation to its handler, dispatching per
// resource kind so no single switch grows unwieldy.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, op string) {
	if h.serveComputeEnvironmentOp(w, r, op) ||
		h.serveJobQueueOp(w, r, op) ||
		h.serveJobDefinitionOp(w, r, op) {
		return
	}

	writeError(w, http.StatusBadRequest, exceptionClient, "unsupported operation: "+op)
}

// serveComputeEnvironmentOp handles the compute-environment operations, or
// returns false if op is not one of them.
func (h *Handler) serveComputeEnvironmentOp(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case opCreateComputeEnvironment:
		h.createComputeEnvironment(w, r)
	case opDescribeComputeEnvironments:
		h.describeComputeEnvironments(w, r)
	case opUpdateComputeEnvironment:
		h.updateComputeEnvironment(w, r)
	case opDeleteComputeEnvironment:
		h.deleteComputeEnvironment(w, r)
	default:
		return false
	}

	return true
}

// serveJobQueueOp handles the job-queue operations, or returns false if op is
// not one of them.
func (h *Handler) serveJobQueueOp(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case opCreateJobQueue:
		h.createJobQueue(w, r)
	case opDescribeJobQueues:
		h.describeJobQueues(w, r)
	case opUpdateJobQueue:
		h.updateJobQueue(w, r)
	case opDeleteJobQueue:
		h.deleteJobQueue(w, r)
	default:
		return false
	}

	return true
}

// serveJobDefinitionOp handles the job-definition operations, or returns false
// if op is not one of them.
func (h *Handler) serveJobDefinitionOp(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case opRegisterJobDefinition:
		h.registerJobDefinition(w, r)
	case opDescribeJobDefinitions:
		h.describeJobDefinitions(w, r)
	case opDeregisterJobDefinition:
		h.deregisterJobDefinition(w, r)
	default:
		return false
	}

	return true
}

// splitPath splits a URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}

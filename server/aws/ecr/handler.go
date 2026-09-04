// Package ecr implements the AWS ECR JSON-RPC protocol as a server.Handler.
// Point the real aws-sdk-go-v2 ECR client at a Server registered with this
// handler and repository/image operations work against an in-memory
// containerregistry driver.
//
// ECR uses the AWS JSON 1.1 wire shape (POST + JSON body, dispatched on the
// X-Amz-Target header), the same family as DynamoDB.
package ecr

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const targetPrefix = "AmazonEC2ContainerRegistry_V20150921."

// authTokenProvider is the AWS-specific GetAuthorizationToken surface. ECR
// registry auth is not part of the portable ContainerRegistry driver (Azure
// ACR and GCP Artifact Registry authenticate differently), so the handler
// type-asserts for it rather than widening the shared interface.
type authTokenProvider interface {
	GetAuthorizationToken(ctx context.Context) (token, proxyEndpoint string, expiresAt time.Time, err error)
}

// Handler serves ECR JSON-RPC requests against a ContainerRegistry driver.
type Handler struct {
	registry crdriver.ContainerRegistry

	// previewMu guards previews, the Start/GetLifecyclePolicyPreview result
	// cache. Real ECR splits that evaluation into an asynchronous Start call
	// and a polled Get call; the emulator evaluates synchronously in Start and
	// Get simply replays the cached result, keyed by repository name. This is
	// wire-flow state (an artifact of the two-call protocol), not resource
	// state, so it lives here rather than in the provider/persist layer.
	previewMu sync.Mutex
	previews  map[string]lifecyclePreviewCache
}

// New returns an ECR handler backed by reg.
func New(reg crdriver.ContainerRegistry) *Handler {
	return &Handler{registry: reg, previews: make(map[string]lifecyclePreviewCache)}
}

// Matches returns true for ECR-shaped requests, identified by an X-Amz-Target
// header of "AmazonEC2ContainerRegistry_V20150921.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches ECR operations based on X-Amz-Target. The dispatch is
// split across route* methods, grouped by area (repositories, images, tags,
// lifecycle policies, scanning), so the operation count can grow without
// tripping the function-length lint limit.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if h.routeRepositories(w, r, op) || h.routeImages(w, r, op) ||
		h.routeTags(w, r, op) || h.routeLifecycle(w, r, op) || h.routeScanning(w, r, op) {
		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException", "unknown ECR operation: "+op)
}

// routeRepositories dispatches repository-management operations.
func (h *Handler) routeRepositories(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateRepository":
		h.createRepository(w, r)
	case "DescribeRepositories":
		h.describeRepositories(w, r)
	case "DeleteRepository":
		h.deleteRepository(w, r)
	case "PutImageTagMutability":
		h.putImageTagMutability(w, r)
	case "PutImageScanningConfiguration":
		h.putImageScanningConfiguration(w, r)
	default:
		return false
	}

	return true
}

// routeImages dispatches image push/list/describe/delete operations.
func (h *Handler) routeImages(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutImage":
		h.putImage(w, r)
	case "ListImages":
		h.listImages(w, r)
	case "DescribeImages":
		h.describeImages(w, r)
	case "BatchDeleteImage":
		h.batchDeleteImage(w, r)
	case "BatchGetImage":
		h.batchGetImage(w, r)
	default:
		return false
	}

	return true
}

// routeTags dispatches resource tagging and repository-policy operations.
func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
	case "SetRepositoryPolicy":
		h.setRepositoryPolicy(w, r)
	case "GetRepositoryPolicy":
		h.getRepositoryPolicy(w, r)
	case "DeleteRepositoryPolicy":
		h.deleteRepositoryPolicy(w, r)
	default:
		return false
	}

	return true
}

// routeLifecycle dispatches lifecycle-policy CRUD and preview operations.
func (h *Handler) routeLifecycle(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutLifecyclePolicy":
		h.putLifecyclePolicy(w, r)
	case "GetLifecyclePolicy":
		h.getLifecyclePolicy(w, r)
	case "DeleteLifecyclePolicy":
		h.deleteLifecyclePolicy(w, r)
	case "StartLifecyclePolicyPreview":
		h.startLifecyclePolicyPreview(w, r)
	case "GetLifecyclePolicyPreview":
		h.getLifecyclePolicyPreview(w, r)
	default:
		return false
	}

	return true
}

// routeScanning dispatches image-scanning and registry-auth operations.
func (h *Handler) routeScanning(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "GetAuthorizationToken":
		h.getAuthorizationToken(w, r)
	case "StartImageScan":
		h.startImageScan(w, r)
	case "DescribeImageScanFindings":
		h.describeImageScanFindings(w, r)
	default:
		return false
	}

	return true
}

// getAuthorizationToken returns a docker-login credential. The response shape
// matches the AWS SDK's AuthorizationData: a base64 token, an expiry, and the
// registry proxy endpoint.
func (h *Handler) getAuthorizationToken(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.registry.(authTokenProvider)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"ServerException", "authorization token not supported")
		return
	}

	token, endpoint, expiresAt, err := auth.GetAuthorizationToken(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"authorizationData": []map[string]any{{
			"authorizationToken": token,
			"proxyEndpoint":      endpoint,
			"expiresAt":          expiresAt.Unix(),
		}},
	})
}

// writePutImageErr maps PutImage errors. On an IMMUTABLE repository, re-pushing
// an existing tag surfaces as FailedPrecondition from the driver; real ECR
// returns ImageTagAlreadyExistsException (not RepositoryNotEmptyException).
func writePutImageErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) {
		wire.WriteJSONError(w, http.StatusBadRequest, "ImageTagAlreadyExistsException", cerrors.Message(err))
		return
	}

	writeErr(w, err)
}

// writeErr maps canonical cloudemu errors to ECR JSON error responses. ECR
// returns errors as HTTP 400 with a "__type" body the SDK maps to a typed
// exception.
func writeErr(w http.ResponseWriter, err error) {
	// A provider error may carry a precise ECR exception name (e.g.
	// ImageNotFoundException vs ScanNotFoundException vs
	// RepositoryPolicyNotFoundException), which the generic code-based mapping
	// below would otherwise collapse to RepositoryNotFoundException.
	msg := cerrors.Message(err)

	var ex interface{ ECRException() string }
	if stderrors.As(err, &ex) {
		wire.WriteJSONError(w, http.StatusBadRequest, ex.ECRException(), msg)
		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryAlreadyExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryNotEmptyException", msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServerException", msg)
	}
}

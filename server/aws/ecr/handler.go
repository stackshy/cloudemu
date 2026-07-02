// Package ecr implements the AWS ECR JSON-RPC protocol as a server.Handler.
// Point the real aws-sdk-go-v2 ECR client at a Server registered with this
// handler and repository/image operations work against an in-memory
// containerregistry driver.
//
// ECR uses the AWS JSON 1.1 wire shape (POST + JSON body, dispatched on the
// X-Amz-Target header), the same family as DynamoDB.
package ecr

import (
	"net/http"
	"strings"

	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire"
)

const targetPrefix = "AmazonEC2ContainerRegistry_V20150921."

// Handler serves ECR JSON-RPC requests against a ContainerRegistry driver.
type Handler struct {
	registry crdriver.ContainerRegistry
}

// New returns an ECR handler backed by reg.
func New(reg crdriver.ContainerRegistry) *Handler {
	return &Handler{registry: reg}
}

// Matches returns true for ECR-shaped requests, identified by an X-Amz-Target
// header of "AmazonEC2ContainerRegistry_V20150921.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches ECR operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix) {
	case "CreateRepository":
		h.createRepository(w, r)
	case "DescribeRepositories":
		h.describeRepositories(w, r)
	case "DeleteRepository":
		h.deleteRepository(w, r)
	case "PutImage":
		h.putImage(w, r)
	case "ListImages":
		h.listImages(w, r)
	case "DescribeImages":
		h.describeImages(w, r)
	case "BatchDeleteImage":
		h.batchDeleteImage(w, r)
	default:
		op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown ECR operation: "+op)
	}
}

// writeErr maps canonical cloudemu errors to ECR JSON error responses. ECR
// returns errors as HTTP 400 with a "__type" body the SDK maps to a typed
// exception.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryAlreadyExistsException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "RepositoryNotEmptyException", err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServerException", err.Error())
	}
}

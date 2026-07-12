// Package secretsmanager implements the AWS Secrets Manager JSON-RPC protocol
// as a server.Handler. Point the real aws-sdk-go-v2 Secrets Manager client at
// a Server registered with this handler and secret/version operations work
// against an in-memory secrets driver.
//
// Secrets Manager uses the AWS JSON 1.1 wire shape (POST + JSON body,
// dispatched on the X-Amz-Target header), the same family as DynamoDB and ECR.
package secretsmanager

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const targetPrefix = "secretsmanager."

// Handler serves Secrets Manager JSON-RPC requests against a Secrets driver.
type Handler struct {
	secrets secretsdriver.Secrets
}

// New returns a Secrets Manager handler backed by s.
func New(s secretsdriver.Secrets) *Handler {
	return &Handler{secrets: s}
}

// Matches returns true for Secrets Manager-shaped requests, identified by an
// X-Amz-Target header of "secretsmanager.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Secrets Manager operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix) {
	case "CreateSecret":
		h.createSecret(w, r)
	case "DeleteSecret":
		h.deleteSecret(w, r)
	case "DescribeSecret":
		h.describeSecret(w, r)
	case "ListSecrets":
		h.listSecrets(w, r)
	case "GetSecretValue":
		h.getSecretValue(w, r)
	case "PutSecretValue":
		h.putSecretValue(w, r)
	case "ListSecretVersionIds":
		h.listSecretVersionIDs(w, r)
	default:
		op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown Secrets Manager operation: "+op)
	}
}

// writeErr maps canonical cloudemu errors to Secrets Manager JSON error
// responses. Secrets Manager returns errors as HTTP 400 with a "__type" body
// the SDK maps to a typed exception.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceExistsException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServiceError", err.Error())
	}
}

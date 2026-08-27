// Package secretsmanager implements the AWS Secrets Manager JSON-RPC protocol
// as a server.Handler. Point the real aws-sdk-go-v2 Secrets Manager client at
// a Server registered with this handler and secret/version operations work
// against an in-memory secrets driver.
//
// Secrets Manager uses the AWS JSON 1.1 wire shape (POST + JSON body,
// dispatched on the X-Amz-Target header), the same family as DynamoDB and ECR.
package secretsmanager

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const targetPrefix = "secretsmanager."

// secretMutator is the AWS-specific UpdateSecret + tagging surface. These are
// not part of the portable Secrets driver (Azure Key Vault and GCP Secret
// Manager also implement it), so the handler type-asserts for them rather than
// widening the shared interface.
type secretMutator interface {
	UpdateSecret(ctx context.Context, name, description string, value []byte) (*secretsdriver.SecretInfo, string, error)
	TagSecret(ctx context.Context, name string, tags map[string]string) error
	UntagSecret(ctx context.Context, name string, keys []string) error
}

// secretStager is the AWS-specific staging/rotation/soft-delete surface the
// handler type-asserts for (implemented by the AWS provider), keeping the
// portable Secrets driver minimal so Azure/GCP are unaffected.
type secretStager interface {
	MarkVersionBinary(ctx context.Context, name, versionID string) error
	GetSecretValueStage(ctx context.Context, name, versionID, stage string) (*secretsdriver.SecretVersion, error)
	SecretVersionStages(ctx context.Context, name string) (map[string][]string, error)
	SecretDeletionDate(ctx context.Context, name string) (string, bool)
	SecretMetadata(ctx context.Context, name string) (*secretsdriver.SecretInfo, error)
	DeleteSecretWithOptions(
		ctx context.Context, name string, recoveryWindow *int64, force bool,
	) (*secretsdriver.SecretInfo, string, error)
	RestoreSecret(ctx context.Context, name string) (*secretsdriver.SecretInfo, error)
	RotateSecret(ctx context.Context, name string) (*secretsdriver.SecretVersion, error)
	PutSecretValueStaged(
		ctx context.Context, name string, value []byte, clientRequestToken string, versionStages []string,
	) (*secretsdriver.SecretVersion, error)
	UpdateSecretVersionStage(
		ctx context.Context, name, versionStage, removeFrom, moveTo string,
	) (*secretsdriver.SecretInfo, error)
}

// secretPolicyManager is the AWS-specific resource-based-policy surface the
// handler type-asserts for (implemented by the AWS provider), keeping the
// portable Secrets driver minimal so Azure/GCP are unaffected.
type secretPolicyManager interface {
	PutResourcePolicy(ctx context.Context, name, policy string) (*secretsdriver.SecretInfo, error)
	GetResourcePolicy(ctx context.Context, name string) (*secretsdriver.SecretInfo, string, error)
	DeleteResourcePolicy(ctx context.Context, name string) (*secretsdriver.SecretInfo, error)
}

// Handler serves Secrets Manager JSON-RPC requests against a Secrets driver.
type Handler struct {
	secrets secretsdriver.Secrets
	routes  map[string]http.HandlerFunc
}

// New returns a Secrets Manager handler backed by s.
func New(s secretsdriver.Secrets) *Handler {
	h := &Handler{secrets: s}
	h.routes = map[string]http.HandlerFunc{
		"CreateSecret":             h.createSecret,
		"DeleteSecret":             h.deleteSecret,
		"DescribeSecret":           h.describeSecret,
		"GetResourcePolicy":        h.getResourcePolicy,
		"PutResourcePolicy":        h.putResourcePolicy,
		"DeleteResourcePolicy":     h.deleteResourcePolicy,
		"ValidateResourcePolicy":   h.validateResourcePolicy,
		"ListSecrets":              h.listSecrets,
		"GetSecretValue":           h.getSecretValue,
		"BatchGetSecretValue":      h.batchGetSecretValue,
		"PutSecretValue":           h.putSecretValue,
		"ListSecretVersionIds":     h.listSecretVersionIDs,
		"UpdateSecret":             h.updateSecret,
		"UpdateSecretVersionStage": h.updateSecretVersionStage,
		"RestoreSecret":            h.restoreSecret,
		"RotateSecret":             h.rotateSecret,
		"GetRandomPassword":        h.getRandomPassword,
		"TagResource":              h.tagResource,
		"UntagResource":            h.untagResource,
	}

	return h
}

// Matches returns true for Secrets Manager-shaped requests, identified by an
// X-Amz-Target header of "secretsmanager.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Secrets Manager operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest,
		"UnknownOperationException", "unknown Secrets Manager operation: "+op)
}

// errNotSupported is returned when the backing driver doesn't implement the
// AWS-specific secretMutator surface. Real deployments always do.
var errNotSupported = cerrors.New(cerrors.Unimplemented, "operation not supported by this backend")

// writeErr maps canonical cloudemu errors to Secrets Manager JSON error
// responses. Secrets Manager returns errors as HTTP 400 with a "__type" body
// the SDK maps to a typed exception.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServiceError", msg)
	}
}

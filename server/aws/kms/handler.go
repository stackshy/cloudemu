// Package kms implements the AWS KMS JSON 1.1 protocol as a server.Handler.
// Point the real aws-sdk-go-v2 KMS client (or the `aws kms` CLI) at a Server
// registered with this handler and key, alias, tag, and (in later phases)
// cryptographic operations run against an in-memory KMS driver.
//
// KMS uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, whose service prefix is the historical "TrentService.").
package kms

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

const targetPrefix = "TrentService."

// Handler serves KMS JSON-RPC requests against a KMS driver.
type Handler struct {
	kms    kmsdriver.KMS
	routes map[string]http.HandlerFunc
}

// New returns a KMS handler backed by k.
func New(k kmsdriver.KMS) *Handler {
	h := &Handler{kms: k}
	h.routes = map[string]http.HandlerFunc{
		"CreateKey":            h.createKey,
		"DescribeKey":          h.describeKey,
		"ListKeys":             h.listKeys,
		"EnableKey":            h.enableKey,
		"DisableKey":           h.disableKey,
		"UpdateKeyDescription": h.updateKeyDescription,
		"ScheduleKeyDeletion":  h.scheduleKeyDeletion,
		"CancelKeyDeletion":    h.cancelKeyDeletion,
		"CreateAlias":          h.createAlias,
		"UpdateAlias":          h.updateAlias,
		"DeleteAlias":          h.deleteAlias,
		"ListAliases":          h.listAliases,
		"TagResource":          h.tagResource,
		"UntagResource":        h.untagResource,
		"ListResourceTags":     h.listResourceTags,

		"Encrypt":                             h.encrypt,
		"Decrypt":                             h.decrypt,
		"ReEncrypt":                           h.reEncrypt,
		"GenerateDataKey":                     h.generateDataKey,
		"GenerateDataKeyWithoutPlaintext":     h.generateDataKeyWithoutPlaintext,
		"GenerateDataKeyPair":                 h.generateDataKeyPair,
		"GenerateDataKeyPairWithoutPlaintext": h.generateDataKeyPairWithoutPlaintext,
		"GenerateRandom":                      h.generateRandom,
		"GetPublicKey":                        h.getPublicKey,
		"Sign":                                h.sign,
		"Verify":                              h.verify,
		"GenerateMac":                         h.generateMac,
		"VerifyMac":                           h.verifyMac,

		"CreateGrant":          h.createGrant,
		"ListGrants":           h.listGrants,
		"RevokeGrant":          h.revokeGrant,
		"RetireGrant":          h.retireGrant,
		"ListRetirableGrants":  h.listRetirableGrants,
		"EnableKeyRotation":    h.enableKeyRotation,
		"DisableKeyRotation":   h.disableKeyRotation,
		"GetKeyRotationStatus": h.getKeyRotationStatus,
		"ListKeyRotations":     h.listKeyRotations,
		"RotateKeyOnDemand":    h.rotateKeyOnDemand,
		"GetKeyPolicy":         h.getKeyPolicy,
		"PutKeyPolicy":         h.putKeyPolicy,
		"ListKeyPolicies":      h.listKeyPolicies,

		"GetParametersForImport":    h.getParametersForImport,
		"ImportKeyMaterial":         h.importKeyMaterial,
		"DeleteImportedKeyMaterial": h.deleteImportedKeyMaterial,
		"ReplicateKey":              h.replicateKey,
		"UpdatePrimaryRegion":       h.updatePrimaryRegion,
	}

	return h
}

// Matches returns true for KMS-shaped requests, identified by an X-Amz-Target
// header of "TrentService.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches KMS operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported KMS operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error). It collapses the identical
// decode/call/respond boilerplate every operation would otherwise repeat.
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, out)
}

// sentinelException maps a driver sentinel error to the exact KMS exception the
// aws-sdk-go-v2 client models, so callers can errors.As the typed exception.
// Returns "" when err is not a recognized sentinel.
func sentinelException(err error) string {
	switch {
	case errors.Is(err, kmsdriver.ErrSignatureInvalid):
		return "KMSInvalidSignatureException"
	case errors.Is(err, kmsdriver.ErrMacInvalid):
		return "KMSInvalidMacException"
	case errors.Is(err, kmsdriver.ErrInvalidCiphertext):
		return "InvalidCiphertextException"
	case errors.Is(err, kmsdriver.ErrIncorrectKey):
		return "IncorrectKeyException"
	case errors.Is(err, kmsdriver.ErrKeyDisabled):
		return "DisabledException"
	case errors.Is(err, kmsdriver.ErrKeyInvalidState):
		return "KMSInvalidStateException"
	case errors.Is(err, kmsdriver.ErrInvalidKeyUsage):
		return "InvalidKeyUsageException"
	case errors.Is(err, kmsdriver.ErrUnsupportedOperation):
		return "UnsupportedOperationException"
	default:
		return ""
	}
}

// writeErr maps a driver error to the closest KMS JSON error type. Sentinel
// errors take precedence so distinct crypto/state failures reach the client as
// their real typed exceptions rather than a generic ValidationException.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	if exc := sentinelException(err); exc != "" {
		wire.WriteJSONError(w, http.StatusBadRequest, exc, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "NotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "AlreadyExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "KMSInvalidStateException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "KMSInternalException", msg)
	}
}

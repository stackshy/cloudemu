// Package kms implements the AWS KMS JSON 1.1 protocol as a server.Handler.
// Point the real aws-sdk-go-v2 KMS client (or the `aws kms` CLI) at a Server
// registered with this handler and key, alias, tag, and (in later phases)
// cryptographic operations run against an in-memory KMS driver.
//
// KMS uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, whose service prefix is the historical "TrentService.").
package kms

import (
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

// writeErr maps a driver error to the closest KMS JSON error type.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "NotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "AlreadyExistsException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "KMSInvalidStateException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "KMSInternalException", err.Error())
	}
}

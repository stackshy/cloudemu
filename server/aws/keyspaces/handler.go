// Package keyspaces implements the Amazon Keyspaces control-plane API as a
// server.Handler. Keyspaces uses AWS JSON 1.0 with the X-Amz-Target header
// prefix "KeyspacesService.", so a real aws-sdk-go-v2/service/keyspaces client
// configured with a custom endpoint hits this handler unchanged.
package keyspaces

import (
	stderrors "errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

const targetPrefix = "KeyspacesService."

// Handler serves Keyspaces requests against a keyspaces driver.
type Handler struct {
	db ksdriver.Keyspaces
}

// New returns a Keyspaces handler backed by db.
func New(db ksdriver.Keyspaces) *Handler {
	return &Handler{db: db}
}

// Matches claims requests whose X-Amz-Target names a Keyspaces operation.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches on the operation named in X-Amz-Target.
//
//nolint:gocyclo // a flat operation switch is the clearest dispatch shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "CreateKeyspace":
		h.createKeyspace(w, r)
	case "GetKeyspace":
		h.getKeyspace(w, r)
	case "ListKeyspaces":
		h.listKeyspaces(w, r)
	case "UpdateKeyspace":
		h.updateKeyspace(w, r)
	case "DeleteKeyspace":
		h.deleteKeyspace(w, r)
	case "CreateTable":
		h.createTable(w, r)
	case "GetTable":
		h.getTable(w, r)
	case "ListTables":
		h.listTables(w, r)
	case "UpdateTable":
		h.updateTable(w, r)
	case "DeleteTable":
		h.deleteTable(w, r)
	case "RestoreTable":
		h.restoreTable(w, r)
	case "CreateType":
		h.createType(w, r)
	case "GetType":
		h.getType(w, r)
	case "ListTypes":
		h.listTypes(w, r)
	case "DeleteType":
		h.deleteType(w, r)
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
	default:
		if h.serveOptional(w, r, op) {
			return
		}

		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", "unknown Keyspaces operation: "+op)
	}
}

// writeErr maps a canonical error to a Keyspaces exception.
func writeErr(w http.ResponseWriter, err error) {
	msg := wireMessage(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err), cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ConflictException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerException", msg)
	}
}

func wireMessage(err error) string {
	var ce *cerrors.Error
	if stderrors.As(err, &ce) {
		return ce.Message
	}

	return err.Error()
}

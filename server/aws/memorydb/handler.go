// Package memorydb implements the AWS MemoryDB control-plane API as a
// server.Handler. MemoryDB uses AWS JSON 1.1 with the X-Amz-Target header
// prefix "AmazonMemoryDB.", so real aws-sdk-go-v2 memorydb clients configured
// with a custom endpoint hit this handler unchanged.
package memorydb

import (
	stderrors "errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

const targetPrefix = "AmazonMemoryDB."

// Handler serves MemoryDB requests against a memorydb driver.
type Handler struct {
	db mdbdriver.MemoryDB
}

// New returns a MemoryDB handler backed by db.
func New(db mdbdriver.MemoryDB) *Handler {
	return &Handler{db: db}
}

// Matches claims requests whose X-Amz-Target names a MemoryDB operation.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches on the operation named in X-Amz-Target.
//
//nolint:gocyclo,funlen // a flat operation switch is the clearest dispatch shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "CreateCluster":
		h.createCluster(w, r)
	case "DescribeClusters":
		h.describeClusters(w, r)
	case "UpdateCluster":
		h.updateCluster(w, r)
	case "DeleteCluster":
		h.deleteCluster(w, r)
	case "FailoverShard":
		h.failoverShard(w, r)
	case "ListAllowedNodeTypeUpdates":
		h.listAllowedNodeTypeUpdates(w, r)
	case "CreateACL":
		h.createACL(w, r)
	case "DescribeACLs":
		h.describeACLs(w, r)
	case "UpdateACL":
		h.updateACL(w, r)
	case "DeleteACL":
		h.deleteACL(w, r)
	case "CreateUser":
		h.createUser(w, r)
	case "DescribeUsers":
		h.describeUsers(w, r)
	case "UpdateUser":
		h.updateUser(w, r)
	case "DeleteUser":
		h.deleteUser(w, r)
	case "CreateParameterGroup":
		h.createParameterGroup(w, r)
	case "DescribeParameterGroups":
		h.describeParameterGroups(w, r)
	case "UpdateParameterGroup":
		h.updateParameterGroup(w, r)
	case "ResetParameterGroup":
		h.resetParameterGroup(w, r)
	case "DeleteParameterGroup":
		h.deleteParameterGroup(w, r)
	case "DescribeParameters":
		h.describeParameters(w, r)
	case "CreateSubnetGroup":
		h.createSubnetGroup(w, r)
	case "DescribeSubnetGroups":
		h.describeSubnetGroups(w, r)
	case "UpdateSubnetGroup":
		h.updateSubnetGroup(w, r)
	case "DeleteSubnetGroup":
		h.deleteSubnetGroup(w, r)
	case "CreateSnapshot":
		h.createSnapshot(w, r)
	case "DescribeSnapshots":
		h.describeSnapshots(w, r)
	case "CopySnapshot":
		h.copySnapshot(w, r)
	case "DeleteSnapshot":
		h.deleteSnapshot(w, r)
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTags":
		h.listTags(w, r)
	case "DescribeEngineVersions":
		h.describeEngineVersions(w, r)
	case "DescribeEvents":
		h.describeEvents(w, r)
	default:
		if h.serveOptional(w, r, op) {
			return
		}

		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValueException", "unknown MemoryDB operation: "+op)
	}
}

// writeErr maps a canonical error to a MemoryDB fault for the given resource
// (e.g. resource="Cluster" → ClusterNotFoundFault / ClusterAlreadyExistsFault).
func writeErr(w http.ResponseWriter, resource string, err error) {
	msg := wireMessage(err)

	// A referenced sibling resource (ACL / subnet group / parameter group) that
	// does not exist carries its own kind, so it maps to the specific
	// <Kind>NotFoundFault the SDK models rather than the operation's resource.
	var nf *mdbdriver.NotFoundError
	if stderrors.As(err, &nf) {
		wire.WriteJSONError(w, http.StatusBadRequest, nf.Kind+"NotFoundFault", msg)
		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, resource+"NotFoundFault", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, resource+"AlreadyExistsFault", msg)
	case cerrors.IsInvalidArgument(err), cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValueException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerErrorException", msg)
	}
}

func wireMessage(err error) string {
	var ce *cerrors.Error
	if stderrors.As(err, &ce) {
		return ce.Message
	}

	return err.Error()
}

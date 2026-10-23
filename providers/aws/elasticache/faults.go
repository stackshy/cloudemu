package elasticache

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ElastiCache wire fault codes, as the AWS API (botocore error.code) spells
// them. A caller matching the SDK's typed errors sees this code, so it travels
// on the error as a typed field rather than inside the message text.
const (
	FaultCacheClusterNotFound             = "CacheClusterNotFound"
	FaultCacheClusterAlreadyExists        = "CacheClusterAlreadyExists"
	FaultInvalidCacheClusterState         = "InvalidCacheClusterState"
	FaultReplicationGroupNotFound         = "ReplicationGroupNotFoundFault"
	FaultReplicationGroupAlreadyExists    = "ReplicationGroupAlreadyExists"
	FaultInvalidReplicationGroupState     = "InvalidReplicationGroupState"
	FaultCacheSubnetGroupNotFound         = "CacheSubnetGroupNotFoundFault"
	FaultCacheSubnetGroupAlreadyExists    = "CacheSubnetGroupAlreadyExists"
	FaultCacheSubnetGroupInUse            = "CacheSubnetGroupInUse"
	FaultCacheParameterGroupNotFound      = "CacheParameterGroupNotFound"
	FaultCacheParameterGroupAlreadyExists = "CacheParameterGroupAlreadyExists"
	FaultInvalidCacheParameterGroupState  = "InvalidCacheParameterGroupState"
	FaultSnapshotNotFound                 = "SnapshotNotFoundFault"
	FaultSnapshotAlreadyExists            = "SnapshotAlreadyExistsFault"
	FaultSnapshotFeatureNotSupported      = "SnapshotFeatureNotSupportedFault"
)

// Fault is an ElastiCache error that carries its AWS wire fault code apart from
// the human message. Error() keeps the canonical cloudemu form; it unwraps to
// the canonical error, so cerrors.IsNotFound and friends keep working, and
// cerrors.Message returns the message without any code token, which is what
// real ElastiCache puts in <Message>.
type Fault struct {
	err  *cerrors.Error
	code string
}

// Error implements error.
func (f *Fault) Error() string { return f.err.Error() }

// Unwrap returns the canonical cloudemu error.
func (f *Fault) Unwrap() error { return f.err }

// AWSFault returns the ElastiCache wire fault code.
func (f *Fault) AWSFault() string { return f.code }

// newFault builds a Fault of the given canonical kind and wire code.
func newFault(kind cerrors.Code, code, format string, args ...any) *Fault {
	return &Fault{err: cerrors.Newf(kind, format, args...), code: code}
}

func cacheNotFound(name string) *Fault {
	return newFault(cerrors.NotFound, FaultCacheClusterNotFound, "cache cluster %q not found", name)
}

func cacheAlreadyExists(name string) *Fault {
	return newFault(cerrors.AlreadyExists, FaultCacheClusterAlreadyExists, "cache cluster %q already exists", name)
}

// cacheInvalidState reports a mutating call on a cache cluster whose status
// does not allow it (it is still being created or is being deleted).
func cacheInvalidState(name, status string) *Fault {
	return newFault(cerrors.FailedPrecondition, FaultInvalidCacheClusterState,
		"cache cluster %q is not available (status %s)", name, status)
}

func rgNotFound(id string) *Fault {
	return newFault(cerrors.NotFound, FaultReplicationGroupNotFound, "replication group %q not found", id)
}

func rgAlreadyExists(id string) *Fault {
	return newFault(cerrors.AlreadyExists, FaultReplicationGroupAlreadyExists,
		"replication group %q already exists", id)
}

func rgInvalidState(id, status string) *Fault {
	return newFault(cerrors.FailedPrecondition, FaultInvalidReplicationGroupState,
		"replication group %q is not available (status %s)", id, status)
}

func subnetGroupNotFound(name string) *Fault {
	return newFault(cerrors.NotFound, FaultCacheSubnetGroupNotFound, "cache subnet group %q not found", name)
}

func parameterGroupNotFound(name string) *Fault {
	return newFault(cerrors.NotFound, FaultCacheParameterGroupNotFound, "cache parameter group %q not found", name)
}

func snapshotNotFound(name string) *Fault {
	return newFault(cerrors.NotFound, FaultSnapshotNotFound, "snapshot %q not found", name)
}

func snapshotAlreadyExists(name string) *Fault {
	return newFault(cerrors.AlreadyExists, FaultSnapshotAlreadyExists, "snapshot %q already exists", name)
}

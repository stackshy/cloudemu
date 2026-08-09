package driver

// Resource kinds, used to select the precise EFS exception at the wire layer.
// EFS has per-resource typed exceptions (MountTargetNotFound, AccessPointNotFound,
// …), unlike single-exception services, so errors carry the kind they concern.
const (
	KindFileSystem  = "FileSystem"
	KindMountTarget = "MountTarget"
	KindAccessPoint = "AccessPoint"
	KindPolicy      = "Policy"
	KindReplication = "Replication"
)

// ResourceError tags a canonical cloudemu error with the EFS resource kind it
// concerns, so the server can emit the right X-Amzn-Errortype (e.g.
// MountTargetNotFound vs FileSystemNotFound) while GetCode still resolves the
// HTTP status through Unwrap.
type ResourceError struct {
	Kind string
	Err  error
}

func (e *ResourceError) Error() string { return e.Err.Error() }
func (e *ResourceError) Unwrap() error { return e.Err }

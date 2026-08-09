package driver

import "context"

// MountTarget is an EFS mount target in a subnet.
type MountTarget struct {
	OwnerID              string
	MountTargetID        string
	FileSystemID         string
	SubnetID             string
	LifeCycleState       string
	IPAddress            string
	NetworkInterfaceID   string
	AvailabilityZoneID   string
	AvailabilityZoneName string
	VPCID                string
	SecurityGroups       []string
}

// CreateMountTargetInput describes a mount target to create.
type CreateMountTargetInput struct {
	FileSystemID   string
	SubnetID       string
	IPAddress      string
	SecurityGroups []string
}

// AccessPoint is an EFS access point.
type AccessPoint struct {
	ClientToken    string
	Name           string
	AccessPointID  string
	ARN            string
	FileSystemID   string
	OwnerID        string
	LifeCycleState string
	PosixUser      *PosixUser
	RootDirectory  *RootDirectory
	Tags           map[string]string
}

// PosixUser is the POSIX identity an access point enforces.
type PosixUser struct {
	UID           int64
	GID           int64
	SecondaryGIDs []int64
}

// RootDirectory is the access point's root directory + creation info.
type RootDirectory struct {
	Path         string
	CreationInfo *CreationInfo
}

// CreationInfo describes ownership/permissions for a created root directory.
type CreationInfo struct {
	OwnerUID    int64
	OwnerGID    int64
	Permissions string
}

// CreateAccessPointInput describes an access point to create.
type CreateAccessPointInput struct {
	ClientToken   string
	Name          string
	FileSystemID  string
	PosixUser     *PosixUser
	RootDirectory *RootDirectory
	Tags          map[string]string
}

// MountTargets is the mount-target + access-point surface of EFS.
type MountTargets interface {
	CreateMountTarget(ctx context.Context, in CreateMountTargetInput) (*MountTarget, error)
	DeleteMountTarget(ctx context.Context, mountTargetID string) error
	DescribeMountTargets(ctx context.Context, fileSystemID, mountTargetID, accessPointID string) ([]MountTarget, error)
	DescribeMountTargetSecurityGroups(ctx context.Context, mountTargetID string) ([]string, error)
	ModifyMountTargetSecurityGroups(ctx context.Context, mountTargetID string, securityGroups []string) error
}

// AccessPoints is the access-point surface of EFS.
type AccessPoints interface {
	CreateAccessPoint(ctx context.Context, in CreateAccessPointInput) (*AccessPoint, error)
	DeleteAccessPoint(ctx context.Context, accessPointID string) error
	DescribeAccessPoints(ctx context.Context, fileSystemID, accessPointID string) ([]AccessPoint, error)
}

package efs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// CreateMountTarget creates a mount target for a file system in a subnet.
func (m *Mock) CreateMountTarget(_ context.Context, in driver.CreateMountTargetInput) (*driver.MountTarget, error) {
	if in.SubnetID == "" {
		return nil, errors.New(errors.InvalidArgument, "SubnetId is required")
	}

	fd, ok := m.getFS(in.FileSystemID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "file system %q not found", in.FileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	// EFS allows one mount target per Availability Zone. The emulator models a
	// subnet as its own AZ, so reject a second mount target in the same subnet.
	for _, mt := range fd.mountTgts {
		if mt.SubnetID == in.SubnetID {
			return nil, errors.Newf(errors.AlreadyExists,
				"mount target already exists in subnet %q", in.SubnetID)
		}
	}

	id := "fsmt-" + idgen.GenerateID("")
	eni := "eni-" + idgen.GenerateID("")
	az := "us-east-1a"

	mt := &driver.MountTarget{
		OwnerID:              m.opts.AccountID,
		MountTargetID:        id,
		FileSystemID:         fd.fs.FileSystemID,
		SubnetID:             in.SubnetID,
		LifeCycleState:       driver.StateAvailable,
		IPAddress:            ipAddressOrDefault(in.IPAddress),
		NetworkInterfaceID:   eni,
		AvailabilityZoneID:   "use1-az1",
		AvailabilityZoneName: az,
		VPCID:                "vpc-" + idgen.GenerateID(""),
		SecurityGroups:       append([]string(nil), in.SecurityGroups...),
	}

	fd.mountTgts[id] = mt
	//nolint:gosec // mount-target count is tiny, never near int32 max
	fd.fs.NumberOfMountTargets = int32(len(fd.mountTgts))
	m.mtIndex.Set(id, fd.fs.FileSystemID)

	out := *mt

	return &out, nil
}

func ipAddressOrDefault(ip string) string {
	if ip != "" {
		return ip
	}

	return "10.0.0.10"
}

// DeleteMountTarget removes a mount target.
func (m *Mock) DeleteMountTarget(_ context.Context, mountTargetID string) error {
	fsID, ok := m.mtIndex.Get(mountTargetID)
	if !ok {
		return errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		m.mtIndex.Delete(mountTargetID)

		return errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	delete(fd.mountTgts, mountTargetID)
	//nolint:gosec // mount-target count is tiny, never near int32 max
	fd.fs.NumberOfMountTargets = int32(len(fd.mountTgts))

	m.mtIndex.Delete(mountTargetID)

	return nil
}

// DescribeMountTargets lists mount targets by file system, by mount-target id,
// or by access-point id (whose owning file system is used).
func (m *Mock) DescribeMountTargets(
	_ context.Context, fileSystemID, mountTargetID, accessPointID string,
) ([]driver.MountTarget, error) {
	switch {
	case mountTargetID != "":
		return m.mountTargetByID(mountTargetID)
	case accessPointID != "":
		fsID, ok := m.apIndex.Get(accessPointID)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "access point %q not found", accessPointID)
		}

		return m.mountTargetsOfFS(fsID)
	case fileSystemID != "":
		return m.mountTargetsOfFS(fileSystemID)
	default:
		return nil, errors.New(errors.InvalidArgument,
			"one of FileSystemId, MountTargetId, or AccessPointId is required")
	}
}

func (m *Mock) mountTargetByID(mountTargetID string) ([]driver.MountTarget, error) {
	fsID, ok := m.mtIndex.Get(mountTargetID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	mt, ok := fd.mountTgts[mountTargetID]
	if !ok {
		return nil, errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	return []driver.MountTarget{*mt}, nil
}

func (m *Mock) mountTargetsOfFS(fileSystemID string) ([]driver.MountTarget, error) {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	out := make([]driver.MountTarget, 0, len(fd.mountTgts))
	for _, mt := range fd.mountTgts {
		out = append(out, *mt)
	}

	return out, nil
}

// DescribeMountTargetSecurityGroups returns a mount target's security groups.
func (m *Mock) DescribeMountTargetSecurityGroups(_ context.Context, mountTargetID string) ([]string, error) {
	mt, err := m.lookupMountTarget(mountTargetID)
	if err != nil {
		return nil, err
	}

	return append([]string(nil), mt.SecurityGroups...), nil
}

// ModifyMountTargetSecurityGroups replaces a mount target's security groups.
func (m *Mock) ModifyMountTargetSecurityGroups(
	_ context.Context, mountTargetID string, securityGroups []string,
) error {
	fsID, ok := m.mtIndex.Get(mountTargetID)
	if !ok {
		return errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		return errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	mt, ok := fd.mountTgts[mountTargetID]
	if !ok {
		return errors.Newf(errors.NotFound, "mount target %q not found", mountTargetID)
	}

	mt.SecurityGroups = append([]string(nil), securityGroups...)

	return nil
}

func (m *Mock) lookupMountTarget(mountTargetID string) (*driver.MountTarget, error) {
	got, err := m.mountTargetByID(mountTargetID)
	if err != nil {
		return nil, err
	}

	return &got[0], nil
}

// CreateAccessPoint creates an access point on a file system. The access point
// name comes from the request Name if set, else the "Name" tag (matching EFS,
// where the console name is a tag).
func (m *Mock) CreateAccessPoint(_ context.Context, in driver.CreateAccessPointInput) (*driver.AccessPoint, error) {
	fd, ok := m.getFS(in.FileSystemID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "file system %q not found", in.FileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	name := in.Name
	if name == "" {
		name = in.Tags[nameTag]
	}

	id := "fsap-" + idgen.GenerateID("")
	ap := &driver.AccessPoint{
		ClientToken:    in.ClientToken,
		Name:           name,
		AccessPointID:  id,
		ARN:            m.accessPointARN(id),
		FileSystemID:   fd.fs.FileSystemID,
		OwnerID:        m.opts.AccountID,
		LifeCycleState: driver.StateAvailable,
		PosixUser:      in.PosixUser,
		RootDirectory:  rootDirOrDefault(in.RootDirectory),
		Tags:           copyTags(in.Tags),
	}

	fd.accessPts[id] = ap
	m.apIndex.Set(id, fd.fs.FileSystemID)

	out := *ap

	return &out, nil
}

func rootDirOrDefault(rd *driver.RootDirectory) *driver.RootDirectory {
	if rd == nil {
		return &driver.RootDirectory{Path: "/"}
	}

	if rd.Path == "" {
		rd.Path = "/"
	}

	return rd
}

// DeleteAccessPoint removes an access point.
func (m *Mock) DeleteAccessPoint(_ context.Context, accessPointID string) error {
	fsID, ok := m.apIndex.Get(accessPointID)
	if !ok {
		return errors.Newf(errors.NotFound, "access point %q not found", accessPointID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		m.apIndex.Delete(accessPointID)

		return errors.Newf(errors.NotFound, "access point %q not found", accessPointID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	delete(fd.accessPts, accessPointID)
	m.apIndex.Delete(accessPointID)

	return nil
}

// DescribeAccessPoints lists access points by file system or by access-point id.
func (m *Mock) DescribeAccessPoints(
	_ context.Context, fileSystemID, accessPointID string,
) ([]driver.AccessPoint, error) {
	if accessPointID != "" {
		fsID, ok := m.apIndex.Get(accessPointID)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "access point %q not found", accessPointID)
		}

		fd, ok := m.getFS(fsID)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "access point %q not found", accessPointID)
		}

		fd.mu.RLock()
		defer fd.mu.RUnlock()

		ap, ok := fd.accessPts[accessPointID]
		if !ok {
			return nil, errors.Newf(errors.NotFound, "access point %q not found", accessPointID)
		}

		return []driver.AccessPoint{*ap}, nil
	}

	if fileSystemID == "" {
		return m.allAccessPoints(), nil
	}

	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "file system %q not found", fileSystemID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	out := make([]driver.AccessPoint, 0, len(fd.accessPts))
	for _, ap := range fd.accessPts {
		out = append(out, *ap)
	}

	return out, nil
}

func (m *Mock) allAccessPoints() []driver.AccessPoint {
	var out []driver.AccessPoint

	for _, fd := range m.fileSystems.All() {
		fd.mu.RLock()
		for _, ap := range fd.accessPts {
			out = append(out, *ap)
		}
		fd.mu.RUnlock()
	}

	return out
}

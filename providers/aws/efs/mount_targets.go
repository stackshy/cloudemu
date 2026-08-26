package efs

import (
	"context"
	"strconv"
	"strings"

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
		return nil, notFound(driver.KindFileSystem, "file system %q not found", in.FileSystemID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	// EFS allows one mount target per Availability Zone. The emulator models a
	// subnet as its own AZ, so reject a second mount target in the same subnet.
	for _, mt := range fd.mountTgts {
		if mt.SubnetID == in.SubnetID {
			return nil, conflict(driver.KindMountTarget,
				"mount target already exists in subnet %q", in.SubnetID)
		}
	}

	id := "fsmt-" + idgen.GenerateID("")
	eni := "eni-" + idgen.GenerateID("")

	mt := &driver.MountTarget{
		OwnerID:              m.opts.AccountID,
		MountTargetID:        id,
		FileSystemID:         fd.fs.FileSystemID,
		SubnetID:             in.SubnetID,
		LifeCycleState:       driver.StateAvailable,
		IPAddress:            ipAddressOrDefault(in.IPAddress),
		NetworkInterfaceID:   eni,
		AvailabilityZoneID:   azID(m.opts.Region),
		AvailabilityZoneName: m.opts.Region + "a",
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

// regionShortCode builds an AWS-style region short code (e.g. us-east-1 → use1,
// eu-west-1 → euw1) used as the prefix of an AZ id. AWS's real short codes are
// hand-maintained abbreviations; this approximates them by keeping the first
// region token whole and abbreviating the rest to their first letter, which is
// exact for the common single-word regions.
func regionShortCode(region string) string {
	short := ""

	for i, part := range strings.Split(region, "-") {
		switch {
		case part == "":
			continue
		case part[0] >= '0' && part[0] <= '9':
			short += part
		case i == 0:
			short += part
		default:
			short += part[:1]
		}
	}

	return short
}

// azID builds an AWS-style AZ id (e.g. "use1-az1") from a region, defaulting to
// the first AZ. Callers that know the AZ name should use azIDFromName instead.
func azID(region string) string {
	return regionShortCode(region) + "-az1"
}

// azIDFromName maps an AZ name (e.g. "us-west-2b") to its consistent AZ id
// ("usew2-az2") the way real EFS reports it: the region short code, then "-az"
// plus the zone-letter's ordinal (a→1, b→2, …). A name without a trailing
// letter falls back to az1.
func azIDFromName(azName string) string {
	if azName == "" {
		return ""
	}

	last := azName[len(azName)-1]
	if last < 'a' || last > 'z' {
		return regionShortCode(azName) + "-az1"
	}

	region := azName[:len(azName)-1]
	ordinal := int(last-'a') + 1

	return regionShortCode(region) + "-az" + strconv.Itoa(ordinal)
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
		return notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		m.mtIndex.Delete(mountTargetID)

		return notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
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
			return nil, notFound(driver.KindAccessPoint, "access point %q not found", accessPointID)
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
		return nil, notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		return nil, notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
	}

	fd.mu.RLock()
	defer fd.mu.RUnlock()

	mt, ok := fd.mountTgts[mountTargetID]
	if !ok {
		return nil, notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
	}

	return []driver.MountTarget{*mt}, nil
}

func (m *Mock) mountTargetsOfFS(fileSystemID string) ([]driver.MountTarget, error) {
	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return nil, notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
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
		return notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		return notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
	}

	fd.mu.Lock()
	defer fd.mu.Unlock()

	mt, ok := fd.mountTgts[mountTargetID]
	if !ok {
		return notFound(driver.KindMountTarget, "mount target %q not found", mountTargetID)
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
		return nil, notFound(driver.KindFileSystem, "file system %q not found", in.FileSystemID)
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
		PosixUser:      copyPosixUser(in.PosixUser),
		RootDirectory:  rootDirOrDefault(in.RootDirectory),
		Tags:           copyTags(in.Tags),
	}

	fd.accessPts[id] = ap
	m.apIndex.Set(id, fd.fs.FileSystemID)

	out := *ap

	return &out, nil
}

// rootDirOrDefault deep-copies the caller's RootDirectory so later mutation of
// the input can't reach into stored state, defaulting an empty path to "/".
func rootDirOrDefault(rd *driver.RootDirectory) *driver.RootDirectory {
	if rd == nil {
		return &driver.RootDirectory{Path: "/"}
	}

	out := driver.RootDirectory{Path: rd.Path}
	if out.Path == "" {
		out.Path = "/"
	}

	if rd.CreationInfo != nil {
		ci := *rd.CreationInfo
		out.CreationInfo = &ci
	}

	return &out
}

// copyPosixUser deep-copies the caller's PosixUser (including SecondaryGIDs) to
// prevent aliasing between the request input and stored state.
func copyPosixUser(pu *driver.PosixUser) *driver.PosixUser {
	if pu == nil {
		return nil
	}

	out := *pu
	out.SecondaryGIDs = append([]int64(nil), pu.SecondaryGIDs...)

	return &out
}

// DeleteAccessPoint removes an access point.
func (m *Mock) DeleteAccessPoint(_ context.Context, accessPointID string) error {
	fsID, ok := m.apIndex.Get(accessPointID)
	if !ok {
		return notFound(driver.KindAccessPoint, "access point %q not found", accessPointID)
	}

	fd, ok := m.getFS(fsID)
	if !ok {
		m.apIndex.Delete(accessPointID)

		return notFound(driver.KindAccessPoint, "access point %q not found", accessPointID)
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
			return nil, notFound(driver.KindAccessPoint, "access point %q not found", accessPointID)
		}

		fd, ok := m.getFS(fsID)
		if !ok {
			return nil, notFound(driver.KindAccessPoint, "access point %q not found", accessPointID)
		}

		fd.mu.RLock()
		defer fd.mu.RUnlock()

		ap, ok := fd.accessPts[accessPointID]
		if !ok {
			return nil, notFound(driver.KindAccessPoint, "access point %q not found", accessPointID)
		}

		return []driver.AccessPoint{*ap}, nil
	}

	if fileSystemID == "" {
		return m.allAccessPoints(), nil
	}

	fd, ok := m.getFS(fileSystemID)
	if !ok {
		return nil, notFound(driver.KindFileSystem, "file system %q not found", fileSystemID)
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

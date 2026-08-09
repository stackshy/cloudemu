package efs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// --- wire shapes ---

type mountTargetJSON struct {
	OwnerID              string `json:"OwnerId"`
	MountTargetID        string `json:"MountTargetId"`
	FileSystemID         string `json:"FileSystemId"`
	SubnetID             string `json:"SubnetId"`
	LifeCycleState       string `json:"LifeCycleState"`
	IPAddress            string `json:"IpAddress,omitempty"`
	NetworkInterfaceID   string `json:"NetworkInterfaceId,omitempty"`
	AvailabilityZoneID   string `json:"AvailabilityZoneId,omitempty"`
	AvailabilityZoneName string `json:"AvailabilityZoneName,omitempty"`
	VPCID                string `json:"VpcId,omitempty"`
}

func mountTargetToWire(mt *driver.MountTarget) mountTargetJSON {
	return mountTargetJSON{
		OwnerID:              mt.OwnerID,
		MountTargetID:        mt.MountTargetID,
		FileSystemID:         mt.FileSystemID,
		SubnetID:             mt.SubnetID,
		LifeCycleState:       mt.LifeCycleState,
		IPAddress:            mt.IPAddress,
		NetworkInterfaceID:   mt.NetworkInterfaceID,
		AvailabilityZoneID:   mt.AvailabilityZoneID,
		AvailabilityZoneName: mt.AvailabilityZoneName,
		VPCID:                mt.VPCID,
	}
}

type posixUserJSON struct {
	UID           int64   `json:"Uid"`
	GID           int64   `json:"Gid"`
	SecondaryGIDs []int64 `json:"SecondaryGids,omitempty"`
}

type creationInfoJSON struct {
	OwnerUID    int64  `json:"OwnerUid"`
	OwnerGID    int64  `json:"OwnerGid"`
	Permissions string `json:"Permissions"`
}

type rootDirectoryJSON struct {
	Path         string            `json:"Path,omitempty"`
	CreationInfo *creationInfoJSON `json:"CreationInfo,omitempty"`
}

type accessPointJSON struct {
	ClientToken    string             `json:"ClientToken,omitempty"`
	Name           string             `json:"Name,omitempty"`
	AccessPointID  string             `json:"AccessPointId"`
	AccessPointARN string             `json:"AccessPointArn"`
	FileSystemID   string             `json:"FileSystemId"`
	OwnerID        string             `json:"OwnerId"`
	LifeCycleState string             `json:"LifeCycleState"`
	PosixUser      *posixUserJSON     `json:"PosixUser,omitempty"`
	RootDirectory  *rootDirectoryJSON `json:"RootDirectory,omitempty"`
	Tags           []tag              `json:"Tags"`
}

func accessPointToWire(ap *driver.AccessPoint) accessPointJSON {
	out := accessPointJSON{
		ClientToken:    ap.ClientToken,
		Name:           ap.Name,
		AccessPointID:  ap.AccessPointID,
		AccessPointARN: ap.ARN,
		FileSystemID:   ap.FileSystemID,
		OwnerID:        ap.OwnerID,
		LifeCycleState: ap.LifeCycleState,
		Tags:           mapToTags(ap.Tags),
	}

	if ap.PosixUser != nil {
		out.PosixUser = &posixUserJSON{
			UID: ap.PosixUser.UID, GID: ap.PosixUser.GID, SecondaryGIDs: ap.PosixUser.SecondaryGIDs,
		}
	}

	if ap.RootDirectory != nil {
		rd := &rootDirectoryJSON{Path: ap.RootDirectory.Path}
		if ci := ap.RootDirectory.CreationInfo; ci != nil {
			rd.CreationInfo = &creationInfoJSON{
				OwnerUID: ci.OwnerUID, OwnerGID: ci.OwnerGID, Permissions: ci.Permissions,
			}
		}

		out.RootDirectory = rd
	}

	return out
}

func posixUserToDriver(p *posixUserJSON) *driver.PosixUser {
	if p == nil {
		return nil
	}

	return &driver.PosixUser{UID: p.UID, GID: p.GID, SecondaryGIDs: p.SecondaryGIDs}
}

func rootDirToDriver(rd *rootDirectoryJSON) *driver.RootDirectory {
	if rd == nil {
		return nil
	}

	out := &driver.RootDirectory{Path: rd.Path}
	if rd.CreationInfo != nil {
		out.CreationInfo = &driver.CreationInfo{
			OwnerUID: rd.CreationInfo.OwnerUID, OwnerGID: rd.CreationInfo.OwnerGID,
			Permissions: rd.CreationInfo.Permissions,
		}
	}

	return out
}

// --- requests ---

type createMountTargetRequest struct {
	FileSystemID   string   `json:"FileSystemId"`
	SubnetID       string   `json:"SubnetId"`
	IPAddress      string   `json:"IpAddress"`
	SecurityGroups []string `json:"SecurityGroups"`
}

type modifyMTSecurityGroupsRequest struct {
	SecurityGroups []string `json:"SecurityGroups"`
}

type createAccessPointRequest struct {
	ClientToken   string             `json:"ClientToken"`
	Name          string             `json:"Name"`
	FileSystemID  string             `json:"FileSystemId"`
	PosixUser     *posixUserJSON     `json:"PosixUser"`
	RootDirectory *rootDirectoryJSON `json:"RootDirectory"`
	Tags          []tag              `json:"Tags"`
}

// --- responses ---

type describeMountTargetsResponse struct {
	MountTargets []mountTargetJSON `json:"MountTargets"`
	NextMarker   string            `json:"NextMarker,omitempty"`
}

type mtSecurityGroupsResponse struct {
	SecurityGroups []string `json:"SecurityGroups"`
}

type describeAccessPointsResponse struct {
	AccessPoints []accessPointJSON `json:"AccessPoints"`
	NextToken    string            `json:"NextToken,omitempty"`
}

// mtSubSecurityGroups is the /mount-targets/{id}/<sub> segment this handler serves.
const mtSubSecurityGroups = "security-groups"

// serveMountTargets routes /mount-targets and its sub-paths.
func (h *Handler) serveMountTargets(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 0:
		switch r.Method {
		case http.MethodPost:
			h.createMountTarget(w, r)
		case http.MethodGet:
			h.describeMountTargets(w, r)
		default:
			methodNotAllowed(w)
		}
	case len(rest) == 1:
		if r.Method == http.MethodDelete {
			h.deleteMountTarget(w, r, rest[0])
			return
		}

		methodNotAllowed(w)
	case len(rest) == 2 && rest[1] == mtSubSecurityGroups:
		h.serveMTSecurityGroups(w, r, rest[0])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveMTSecurityGroups(w http.ResponseWriter, r *http.Request, mtID string) {
	switch r.Method {
	case http.MethodGet:
		h.describeMTSecurityGroups(w, r, mtID)
	case http.MethodPut:
		h.modifyMTSecurityGroups(w, r, mtID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createMountTarget(w http.ResponseWriter, r *http.Request) {
	var req createMountTargetRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	mt, err := h.efs.CreateMountTarget(r.Context(), driver.CreateMountTargetInput{
		FileSystemID: req.FileSystemID, SubnetID: req.SubnetID,
		IPAddress: req.IPAddress, SecurityGroups: req.SecurityGroups,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, mountTargetToWire(mt))
}

func (h *Handler) describeMountTargets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	mts, err := h.efs.DescribeMountTargets(r.Context(),
		q.Get("FileSystemId"), q.Get("MountTargetId"), q.Get("AccessPointId"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]mountTargetJSON, 0, len(mts))
	for i := range mts {
		out = append(out, mountTargetToWire(&mts[i]))
	}

	writeJSON(w, describeMountTargetsResponse{MountTargets: out})
}

func (h *Handler) deleteMountTarget(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.efs.DeleteMountTarget(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

func (h *Handler) describeMTSecurityGroups(w http.ResponseWriter, r *http.Request, id string) {
	sgs, err := h.efs.DescribeMountTargetSecurityGroups(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, mtSecurityGroupsResponse{SecurityGroups: sgs})
}

func (h *Handler) modifyMTSecurityGroups(w http.ResponseWriter, r *http.Request, id string) {
	var req modifyMTSecurityGroupsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.efs.ModifyMountTargetSecurityGroups(r.Context(), id, req.SecurityGroups); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

// serveAccessPoints routes /access-points and /access-points/{id}.
func (h *Handler) serveAccessPoints(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createAccessPoint(w, r)
		case http.MethodGet:
			h.describeAccessPoints(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		if r.Method == http.MethodDelete {
			h.deleteAccessPoint(w, r, rest[0])
			return
		}

		methodNotAllowed(w)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) createAccessPoint(w http.ResponseWriter, r *http.Request) {
	var req createAccessPointRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ap, err := h.efs.CreateAccessPoint(r.Context(), driver.CreateAccessPointInput{
		ClientToken: req.ClientToken, Name: req.Name, FileSystemID: req.FileSystemID,
		PosixUser: posixUserToDriver(req.PosixUser), RootDirectory: rootDirToDriver(req.RootDirectory),
		Tags: tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// EFS returns 200 for CreateAccessPoint.
	writeJSON(w, accessPointToWire(ap))
}

func (h *Handler) describeAccessPoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	aps, err := h.efs.DescribeAccessPoints(r.Context(), q.Get("FileSystemId"), q.Get("AccessPointId"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]accessPointJSON, 0, len(aps))
	for i := range aps {
		out = append(out, accessPointToWire(&aps[i]))
	}

	writeJSON(w, describeAccessPointsResponse{AccessPoints: out})
}

func (h *Handler) deleteAccessPoint(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.efs.DeleteAccessPoint(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

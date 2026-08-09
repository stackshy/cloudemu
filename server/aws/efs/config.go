package efs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// --- wire shapes ---

type lifecyclePolicyJSON struct {
	TransitionToIA                  string `json:"TransitionToIA,omitempty"`
	TransitionToPrimaryStorageClass string `json:"TransitionToPrimaryStorageClass,omitempty"`
	TransitionToArchive             string `json:"TransitionToArchive,omitempty"`
}

func lifecycleToWire(ps []driver.LifecyclePolicy) []lifecyclePolicyJSON {
	out := make([]lifecyclePolicyJSON, 0, len(ps))
	for i := range ps {
		out = append(out, lifecyclePolicyJSON{
			TransitionToIA:                  ps[i].TransitionToIA,
			TransitionToPrimaryStorageClass: ps[i].TransitionToPrimaryStorageClass,
			TransitionToArchive:             ps[i].TransitionToArchive,
		})
	}

	return out
}

func lifecycleToDriver(ps []lifecyclePolicyJSON) []driver.LifecyclePolicy {
	out := make([]driver.LifecyclePolicy, 0, len(ps))
	for i := range ps {
		out = append(out, driver.LifecyclePolicy{
			TransitionToIA:                  ps[i].TransitionToIA,
			TransitionToPrimaryStorageClass: ps[i].TransitionToPrimaryStorageClass,
			TransitionToArchive:             ps[i].TransitionToArchive,
		})
	}

	return out
}

type lifecycleConfigRequest struct {
	LifecyclePolicies []lifecyclePolicyJSON `json:"LifecyclePolicies"`
}

type lifecycleConfigResponse struct {
	LifecyclePolicies []lifecyclePolicyJSON `json:"LifecyclePolicies"`
}

type backupPolicyJSON struct {
	Status string `json:"Status"`
}

type backupPolicyEnvelope struct {
	BackupPolicy backupPolicyJSON `json:"BackupPolicy"`
}

type destinationJSON struct {
	Status               string   `json:"Status"`
	FileSystemID         string   `json:"FileSystemId"`
	Region               string   `json:"Region,omitempty"`
	AvailabilityZoneName string   `json:"AvailabilityZoneName,omitempty"`
	KMSKeyID             string   `json:"KmsKeyId,omitempty"`
	RoleARN              string   `json:"RoleArn,omitempty"`
	LastReplicatedTime   *float64 `json:"LastReplicatedTimestamp,omitempty"`
	OwnerID              string   `json:"OwnerId,omitempty"`
}

type destinationToCreateJSON struct {
	Region               string `json:"Region"`
	AvailabilityZoneName string `json:"AvailabilityZoneName"`
	KMSKeyID             string `json:"KmsKeyId"`
	FileSystemID         string `json:"FileSystemId"`
	RoleARN              string `json:"RoleArn"`
}

type replicationConfigJSON struct {
	SourceFileSystemID          string            `json:"SourceFileSystemId"`
	SourceFileSystemARN         string            `json:"SourceFileSystemArn"`
	SourceFileSystemRegion      string            `json:"SourceFileSystemRegion"`
	OriginalSourceFileSystemARN string            `json:"OriginalSourceFileSystemArn"`
	CreationTime                *float64          `json:"CreationTime,omitempty"`
	Destinations                []destinationJSON `json:"Destinations"`
	SourceFileSystemOwnerID     string            `json:"SourceFileSystemOwnerId,omitempty"`
}

func replicationToWire(rc *driver.ReplicationConfiguration) replicationConfigJSON {
	dests := make([]destinationJSON, 0, len(rc.Destinations))

	for i := range rc.Destinations {
		d := rc.Destinations[i]
		dests = append(dests, destinationJSON{
			Status: d.Status, FileSystemID: d.FileSystemID, Region: d.Region,
			AvailabilityZoneName: d.AvailabilityZoneName, KMSKeyID: d.KMSKeyID, RoleARN: d.RoleARN,
			LastReplicatedTime: epochOrNil(d.LastReplicatedTime), OwnerID: d.OwnerID,
		})
	}

	return replicationConfigJSON{
		SourceFileSystemID:          rc.SourceFileSystemID,
		SourceFileSystemARN:         rc.SourceFileSystemARN,
		SourceFileSystemRegion:      rc.SourceFileSystemRegion,
		OriginalSourceFileSystemARN: rc.OriginalSourceFileSystemARN,
		CreationTime:                epochOrNil(rc.CreationTime),
		Destinations:                dests,
		SourceFileSystemOwnerID:     rc.SourceFileSystemOwnerID,
	}
}

type createReplicationRequest struct {
	Destinations []destinationToCreateJSON `json:"Destinations"`
}

type describeReplicationResponse struct {
	Replications []replicationConfigJSON `json:"Replications"`
	NextToken    string                  `json:"NextToken,omitempty"`
}

type resourceIDPreferenceJSON struct {
	ResourceIDType string   `json:"ResourceIdType,omitempty"`
	Resources      []string `json:"Resources,omitempty"`
}

type accountPreferencesRequest struct {
	ResourceIDType string `json:"ResourceIdType"`
}

type accountPreferencesResponse struct {
	ResourceIDPreference *resourceIDPreferenceJSON `json:"ResourceIdPreference,omitempty"`
}

// --- operations ---

func (h *Handler) putLifecycleConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	var req lifecycleConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	got, err := h.efs.PutLifecycleConfiguration(r.Context(), id, lifecycleToDriver(req.LifecyclePolicies))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, lifecycleConfigResponse{LifecyclePolicies: lifecycleToWire(got)})
}

func (h *Handler) describeLifecycleConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	got, err := h.efs.DescribeLifecycleConfiguration(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, lifecycleConfigResponse{LifecyclePolicies: lifecycleToWire(got)})
}

func (h *Handler) putBackupPolicy(w http.ResponseWriter, r *http.Request, id string) {
	var req backupPolicyEnvelope
	if !decodeJSON(w, r, &req) {
		return
	}

	status, err := h.efs.PutBackupPolicy(r.Context(), id, req.BackupPolicy.Status)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, backupPolicyEnvelope{BackupPolicy: backupPolicyJSON{Status: status}})
}

func (h *Handler) describeBackupPolicy(w http.ResponseWriter, r *http.Request, id string) {
	status, err := h.efs.DescribeBackupPolicy(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, backupPolicyEnvelope{BackupPolicy: backupPolicyJSON{Status: status}})
}

func (h *Handler) createReplicationConfiguration(w http.ResponseWriter, r *http.Request, sourceID string) {
	var req createReplicationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	dests := make([]driver.DestinationToCreate, 0, len(req.Destinations))
	for i := range req.Destinations {
		dests = append(dests, driver.DestinationToCreate{
			Region: req.Destinations[i].Region, AvailabilityZoneName: req.Destinations[i].AvailabilityZoneName,
			KMSKeyID: req.Destinations[i].KMSKeyID, FileSystemID: req.Destinations[i].FileSystemID,
			RoleARN: req.Destinations[i].RoleARN,
		})
	}

	rc, err := h.efs.CreateReplicationConfiguration(r.Context(), sourceID, dests)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, replicationToWire(rc))
}

func (h *Handler) deleteReplicationConfiguration(w http.ResponseWriter, r *http.Request, sourceID string) {
	if err := h.efs.DeleteReplicationConfiguration(r.Context(), sourceID); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

func (h *Handler) describeReplicationConfigurations(w http.ResponseWriter, r *http.Request, _ string) {
	fsID := r.URL.Query().Get("FileSystemId")

	rcs, err := h.efs.DescribeReplicationConfigurations(r.Context(), fsID)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]replicationConfigJSON, 0, len(rcs))
	for i := range rcs {
		out = append(out, replicationToWire(&rcs[i]))
	}

	writeJSON(w, describeReplicationResponse{Replications: out})
}

func (h *Handler) serveAccountPreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		h.putAccountPreferences(w, r)
	case http.MethodGet:
		h.describeAccountPreferences(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putAccountPreferences(w http.ResponseWriter, r *http.Request) {
	var req accountPreferencesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	pref, err := h.efs.PutAccountPreferences(r.Context(), req.ResourceIDType)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, accountPreferencesResponse{
		ResourceIDPreference: &resourceIDPreferenceJSON{ResourceIDType: pref, Resources: []string{"FILE_SYSTEM", "MOUNT_TARGET"}},
	})
}

func (h *Handler) describeAccountPreferences(w http.ResponseWriter, r *http.Request) {
	pref, err := h.efs.DescribeAccountPreferences(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	if pref == "" {
		writeJSON(w, accountPreferencesResponse{})
		return
	}

	writeJSON(w, accountPreferencesResponse{
		ResourceIDPreference: &resourceIDPreferenceJSON{ResourceIDType: pref, Resources: []string{"FILE_SYSTEM", "MOUNT_TARGET"}},
	})
}

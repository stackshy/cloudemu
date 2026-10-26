package secretsmanager

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// replicateSecretToRegions adds replica Regions to a primary secret.
func (h *Handler) replicateSecretToRegions(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.secrets.(secretReplicator)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req replicateSecretToRegionsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := rep.ReplicateSecretToRegions(r.Context(), resolveSecretID(req.SecretID),
		replicaRegionsToDriver(req.AddReplicaRegions), req.ForceOverwriteReplicaSecret)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, replicationResponse{
		ARN: info.ResourceID, ReplicationStatus: toReplicationStatusJSON(info.ReplicationStatus),
	})
}

// removeRegionsFromReplication deletes replicas of a primary secret.
func (h *Handler) removeRegionsFromReplication(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.secrets.(secretReplicator)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req removeRegionsFromReplicationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := rep.RemoveRegionsFromReplication(r.Context(), resolveSecretID(req.SecretID), req.RemoveReplicaRegions)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, replicationResponse{
		ARN: info.ResourceID, ReplicationStatus: toReplicationStatusJSON(info.ReplicationStatus),
	})
}

// stopReplicationToReplica promotes a replica to a standalone primary.
func (h *Handler) stopReplicationToReplica(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.secrets.(secretReplicator)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := rep.StopReplicationToReplica(r.Context(), resolveSecretID(req.SecretID))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, stopReplicationToReplicaResponse{ARN: info.ResourceID})
}

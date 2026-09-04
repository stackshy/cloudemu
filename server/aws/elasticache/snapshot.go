package elasticache

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// snapshots reports the snapshot capability, if the driver implements it.
func (h *Handler) snapshots() (cachedriver.Snapshots, bool) {
	s, ok := h.cache.(cachedriver.Snapshots)

	return s, ok
}

//nolint:dupl // per-action snapshot handler; the sibling parses a different request and writes a different response envelope
func (h *Handler) createSnapshot(w http.ResponseWriter, r *http.Request) {
	store, ok := h.snapshots()
	if !ok {
		writeUnsupported(w, "snapshots")
		return
	}

	snap, err := store.CreateSnapshot(r.Context(), cachedriver.SnapshotConfig{
		SnapshotName:       r.Form.Get("SnapshotName"),
		CacheClusterID:     r.Form.Get("CacheClusterId"),
		ReplicationGroupID: r.Form.Get("ReplicationGroupId"),
		KmsKeyID:           r.Form.Get("KmsKeyId"),
		Tags:               parseTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createSnapshotResponse{
		Xmlns:    Namespace,
		Result:   snapshotResult{Snapshot: toSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeSnapshots(w http.ResponseWriter, r *http.Request) {
	store, ok := h.snapshots()
	if !ok {
		writeUnsupported(w, "snapshots")
		return
	}

	snaps, err := store.DescribeSnapshots(r.Context(), cachedriver.SnapshotFilter{
		SnapshotName:       r.Form.Get("SnapshotName"),
		CacheClusterID:     r.Form.Get("CacheClusterId"),
		ReplicationGroupID: r.Form.Get("ReplicationGroupId"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]snapshotXML, 0, len(snaps))
	for i := range snaps {
		out = append(out, toSnapshotXML(&snaps[i]))
	}

	awsquery.WriteXMLResponse(w, describeSnapshotsResponse{
		Xmlns:    Namespace,
		Result:   describeSnapshotsResult{Snapshots: snapshotsListXML{Snapshot: out}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // per-action snapshot handler; the sibling parses a different request and writes a different response envelope
func (h *Handler) copySnapshot(w http.ResponseWriter, r *http.Request) {
	store, ok := h.snapshots()
	if !ok {
		writeUnsupported(w, "snapshots")
		return
	}

	snap, err := store.CopySnapshot(r.Context(), cachedriver.CopySnapshotConfig{
		SourceSnapshotName: r.Form.Get("SourceSnapshotName"),
		TargetSnapshotName: r.Form.Get("TargetSnapshotName"),
		TargetBucket:       r.Form.Get("TargetBucket"),
		KmsKeyID:           r.Form.Get("KmsKeyId"),
		Tags:               parseTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copySnapshotResponse{
		Xmlns:    Namespace,
		Result:   snapshotResult{Snapshot: toSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	store, ok := h.snapshots()
	if !ok {
		writeUnsupported(w, "snapshots")
		return
	}

	snap, err := store.DeleteSnapshot(r.Context(), r.Form.Get("SnapshotName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteSnapshotResponse{
		Xmlns:    Namespace,
		Result:   snapshotResult{Snapshot: toSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

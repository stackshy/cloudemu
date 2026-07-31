package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CreateSnapshotInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	s, err := h.db.CreateSnapshot(r.Context(), mdbdriver.CreateSnapshotConfig{
		Name: aws.ToString(in.SnapshotName), ClusterName: aws.ToString(in.ClusterName),
		KmsKeyID: aws.ToString(in.KmsKeyId), Tags: tagMap(in.Tags),
	})
	if err != nil {
		writeErr(w, "Snapshot", err)
		return
	}

	wire.WriteJSON(w, memorydb.CreateSnapshotOutput{Snapshot: toWireSnapshot(s)})
}

func (h *Handler) describeSnapshots(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeSnapshotsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var names []string
	if in.SnapshotName != nil {
		names = []string{aws.ToString(in.SnapshotName)}
	}

	snaps, err := h.db.DescribeSnapshots(r.Context(), names, aws.ToString(in.ClusterName))
	if err != nil {
		writeErr(w, "Snapshot", err)
		return
	}

	page, next, err := paginate(snaps, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, "Snapshot", err)
		return
	}

	out := memorydb.DescribeSnapshotsOutput{NextToken: next}
	for i := range page {
		out.Snapshots = append(out.Snapshots, *toWireSnapshot(&page[i]))
	}

	wire.WriteJSON(w, out)
}

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) copySnapshot(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CopySnapshotInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	s, err := h.db.CopySnapshot(r.Context(), mdbdriver.CopySnapshotConfig{
		SourceName: aws.ToString(in.SourceSnapshotName), TargetName: aws.ToString(in.TargetSnapshotName),
		KmsKeyID: aws.ToString(in.KmsKeyId), Tags: tagMap(in.Tags),
	})
	if err != nil {
		writeErr(w, "Snapshot", err)
		return
	}

	wire.WriteJSON(w, memorydb.CopySnapshotOutput{Snapshot: toWireSnapshot(s)})
}

func (h *Handler) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DeleteSnapshotInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	s, err := h.db.DeleteSnapshot(r.Context(), aws.ToString(in.SnapshotName))
	if err != nil {
		writeErr(w, "Snapshot", err)
		return
	}

	wire.WriteJSON(w, memorydb.DeleteSnapshotOutput{Snapshot: toWireSnapshot(s)})
}

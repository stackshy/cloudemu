package redshift

import (
	"net/http"
	"net/url"
	"strconv"

	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// clusterConfigFromForm pulls the relevant Cluster fields out of a form. Used
// by CreateCluster.
func clusterConfigFromForm(form url.Values) rdbdriver.ClusterConfig {
	return rdbdriver.ClusterConfig{
		ID:                 form.Get("ClusterIdentifier"),
		Engine:             "redshift",
		EngineVersion:      form.Get("ClusterVersion"),
		MasterUsername:     form.Get("MasterUsername"),
		MasterUserPassword: form.Get("MasterUserPassword"),
		DatabaseName:       form.Get("DBName"),
		Port:               formInt(form.Get("Port")),
		VPCSecurityGroups:  awsquery.ListStrings(form, "VpcSecurityGroupIds.VpcSecurityGroupId"),
		SubnetGroupName:    form.Get("ClusterSubnetGroupName"),
		Tags:               parseRedshiftTags(form),
	}
}

// parseRedshiftTags parses Redshift-style Tags.Tag.N.{Key,Value} entries. Some
// SDK versions emit Tags.member.N.* instead, so both shapes are accepted.
func parseRedshiftTags(form url.Values) map[string]string {
	if out := tagsByPrefix(form, "Tags.Tag"); out != nil {
		return out
	}

	return tagsByPrefix(form, "Tags.member")
}

func tagsByPrefix(form url.Values, prefix string) map[string]string {
	indices := awsquery.CollectIndices(form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make(map[string]string, len(indices))

	for _, n := range indices {
		base := prefix + "." + strconv.Itoa(n)
		if k := form.Get(base + ".Key"); k != "" {
			out[k] = form.Get(base + ".Value")
		}
	}

	return out
}

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	cfg := clusterConfigFromForm(r.Form)

	cluster, err := h.db.CreateCluster(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createClusterResponse{
		Xmlns:    Namespace,
		Result:   clusterResult{Cluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeClusters(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("ClusterIdentifier")

	var ids []string
	if id != "" {
		ids = []string{id}
	}

	clusters, err := h.db.DescribeClusters(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := clustersXML{Cluster: make([]clusterXML, 0, len(clusters))}
	for i := range clusters {
		out.Cluster = append(out.Cluster, toClusterXML(&clusters[i]))
	}

	awsquery.WriteXMLResponse(w, describeClustersResponse{
		Xmlns:    Namespace,
		Result:   clustersResult{Clusters: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyCluster(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("ClusterIdentifier")

	input := rdbdriver.ModifyInstanceInput{
		EngineVersion:      form.Get("ClusterVersion"),
		MasterUserPassword: form.Get("MasterUserPassword"),
		Tags:               parseRedshiftTags(form),
	}

	cluster, err := h.db.ModifyCluster(r.Context(), id, input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyClusterResponse{
		Xmlns:    Namespace,
		Result:   clusterResult{Cluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("ClusterIdentifier")

	clusters, err := h.db.DescribeClusters(r.Context(), []string{id})
	if err != nil {
		writeErr(w, err)
		return
	}

	if len(clusters) == 0 {
		writeErr(w, errClusterNotFound(id))
		return
	}

	last := clusters[0]
	last.State = rdbdriver.StateDeleting

	if err := h.db.DeleteCluster(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteClusterResponse{
		Xmlns:    Namespace,
		Result:   clusterResult{Cluster: toClusterXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) rebootCluster(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("ClusterIdentifier")

	if err := h.db.RebootInstance(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	clusters, err := h.db.DescribeClusters(r.Context(), []string{id})
	if err != nil || len(clusters) == 0 {
		writeErr(w, errClusterNotFound(id))
		return
	}

	awsquery.WriteXMLResponse(w, rebootClusterResponse{
		Xmlns:    Namespace,
		Result:   clusterResult{Cluster: toClusterXML(&clusters[0])},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to restoreFromClusterSnapshot but builds a different driver input and emits a different response type.
func (h *Handler) createClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := rdbdriver.ClusterSnapshotConfig{
		ID:        form.Get("SnapshotIdentifier"),
		ClusterID: form.Get("ClusterIdentifier"),
		Tags:      parseRedshiftTags(form),
	}

	snap, err := h.db.CreateClusterSnapshot(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createClusterSnapshotResponse{
		Xmlns:    Namespace,
		Result:   snapshotResult{Snapshot: toSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeClusterSnapshots(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("SnapshotIdentifier")
	cluster := form.Get("ClusterIdentifier")

	var ids []string
	if id != "" {
		ids = []string{id}
	}

	snaps, err := h.db.DescribeClusterSnapshots(r.Context(), ids, cluster)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := snapshotsXML{Snapshot: make([]snapshotXML, 0, len(snaps))}
	for i := range snaps {
		out.Snapshot = append(out.Snapshot, toSnapshotXML(&snaps[i]))
	}

	awsquery.WriteXMLResponse(w, describeClusterSnapshotsResponse{
		Xmlns:    Namespace,
		Result:   snapshotsResult{Snapshots: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("SnapshotIdentifier")

	snaps, err := h.db.DescribeClusterSnapshots(r.Context(), []string{id}, "")
	if err != nil || len(snaps) == 0 {
		writeErr(w, errClusterSnapshotNotFound(id))
		return
	}

	last := snaps[0]

	if err := h.db.DeleteClusterSnapshot(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteClusterSnapshotResponse{
		Xmlns:    Namespace,
		Result:   snapshotResult{Snapshot: toSnapshotXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to createClusterSnapshot but builds a different driver input and emits a different response type.
func (h *Handler) restoreFromClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	input := rdbdriver.RestoreClusterInput{
		NewClusterID: form.Get("ClusterIdentifier"),
		SnapshotID:   form.Get("SnapshotIdentifier"),
		Tags:         parseRedshiftTags(form),
	}

	cluster, err := h.db.RestoreClusterFromSnapshot(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, restoreFromClusterSnapshotResponse{
		Xmlns:    Namespace,
		Result:   clusterResult{Cluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

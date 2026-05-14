package rds

import (
	"net/http"
	"net/url"
	"strconv"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// instanceFromForm pulls the common DBInstance fields out of a form. Used by
// CreateDBInstance and as the basis for ModifyDBInstance.
func instanceConfigFromForm(form url.Values) rdsdriver.InstanceConfig {
	return rdsdriver.InstanceConfig{
		ID:                 form.Get("DBInstanceIdentifier"),
		Engine:             form.Get("Engine"),
		EngineVersion:      form.Get("EngineVersion"),
		InstanceClass:      form.Get("DBInstanceClass"),
		AllocatedStorage:   formInt(form.Get("AllocatedStorage")),
		StorageType:        form.Get("StorageType"),
		MasterUsername:     form.Get("MasterUsername"),
		MasterUserPassword: form.Get("MasterUserPassword"),
		DBName:             form.Get("DBName"),
		Port:               formInt(form.Get("Port")),
		MultiAZ:            formBool(form.Get("MultiAZ")),
		PubliclyAccessible: formBool(form.Get("PubliclyAccessible")),
		VPCSecurityGroups:  awsquery.ListStrings(form, "VpcSecurityGroupIds.VpcSecurityGroupId"),
		SubnetGroupName:    form.Get("DBSubnetGroupName"),
		ClusterID:          form.Get("DBClusterIdentifier"),
		AvailabilityZone:   form.Get("AvailabilityZone"),
		Tags:               parseRDSTags(form),
	}
}

// parseRDSTags parses RDS-style Tags.member.N.{Key,Value} entries. Some SDK
// versions emit Tags.Tag.N.* instead, so both shapes are accepted.
func parseRDSTags(form url.Values) map[string]string {
	if out := tagsByPrefix(form, "Tags.member"); out != nil {
		return out
	}

	return tagsByPrefix(form, "Tags.Tag")
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

func (h *Handler) createDBInstance(w http.ResponseWriter, r *http.Request) {
	cfg := instanceConfigFromForm(r.Form)

	inst, err := h.db.CreateInstance(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors describeDBClusters but operates on instances.
func (h *Handler) describeDBInstances(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBInstanceIdentifier")

	var ids []string
	if id != "" {
		ids = []string{id}
	}

	insts, err := h.db.DescribeInstances(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := dbInstancesXML{DBInstance: make([]dbInstanceXML, 0, len(insts))}
	for i := range insts {
		out.DBInstance = append(out.DBInstance, toInstanceXML(&insts[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBInstancesResponse{
		Xmlns:    Namespace,
		Result:   dbInstancesResult{DBInstances: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBInstance(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("DBInstanceIdentifier")

	input := rdsdriver.ModifyInstanceInput{
		InstanceClass:      form.Get("DBInstanceClass"),
		AllocatedStorage:   formInt(form.Get("AllocatedStorage")),
		EngineVersion:      form.Get("EngineVersion"),
		MasterUserPassword: form.Get("MasterUserPassword"),
		Tags:               parseRDSTags(form),
	}

	if v := form.Get("MultiAZ"); v != "" {
		b := formBool(v)
		input.MultiAZ = &b
	}

	inst, err := h.db.ModifyInstance(r.Context(), id, input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors deleteDBCluster but operates on instances.
func (h *Handler) deleteDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBInstanceIdentifier")

	insts, err := h.db.DescribeInstances(r.Context(), []string{id})
	if err != nil {
		writeErr(w, err)
		return
	}

	if len(insts) == 0 {
		writeErr(w, errInstanceNotFound(id))
		return
	}

	last := insts[0]
	last.State = rdsdriver.StateDeleting

	if err := h.db.DeleteInstance(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to other lifecycle ops; each needs its own response type.
func (h *Handler) startDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBInstanceIdentifier")

	if err := h.db.StartInstance(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	insts, err := h.db.DescribeInstances(r.Context(), []string{id})
	if err != nil || len(insts) == 0 {
		writeErr(w, errInstanceNotFound(id))
		return
	}

	awsquery.WriteXMLResponse(w, startDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&insts[0])},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to other lifecycle ops; each needs its own response type.
func (h *Handler) stopDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBInstanceIdentifier")

	if err := h.db.StopInstance(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	insts, err := h.db.DescribeInstances(r.Context(), []string{id})
	if err != nil || len(insts) == 0 {
		writeErr(w, errInstanceNotFound(id))
		return
	}

	awsquery.WriteXMLResponse(w, stopDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&insts[0])},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to other lifecycle ops; each needs its own response type.
func (h *Handler) rebootDBInstance(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBInstanceIdentifier")

	if err := h.db.RebootInstance(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	insts, err := h.db.DescribeInstances(r.Context(), []string{id})
	if err != nil || len(insts) == 0 {
		writeErr(w, errInstanceNotFound(id))
		return
	}

	awsquery.WriteXMLResponse(w, rebootDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&insts[0])},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) createDBCluster(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := rdsdriver.ClusterConfig{
		ID:                 form.Get("DBClusterIdentifier"),
		Engine:             form.Get("Engine"),
		EngineVersion:      form.Get("EngineVersion"),
		MasterUsername:     form.Get("MasterUsername"),
		MasterUserPassword: form.Get("MasterUserPassword"),
		DatabaseName:       form.Get("DatabaseName"),
		Port:               formInt(form.Get("Port")),
		VPCSecurityGroups:  awsquery.ListStrings(form, "VpcSecurityGroupIds.VpcSecurityGroupId"),
		SubnetGroupName:    form.Get("DBSubnetGroupName"),
		Tags:               parseRDSTags(form),
	}

	cluster, err := h.db.CreateCluster(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBClusterResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors describeDBInstances but operates on clusters.
func (h *Handler) describeDBClusters(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBClusterIdentifier")

	var ids []string
	if id != "" {
		ids = []string{id}
	}

	clusters, err := h.db.DescribeClusters(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := dbClustersXML{DBCluster: make([]dbClusterXML, 0, len(clusters))}
	for i := range clusters {
		out.DBCluster = append(out.DBCluster, toClusterXML(&clusters[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBClustersResponse{
		Xmlns:    Namespace,
		Result:   dbClustersResult{DBClusters: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBCluster(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("DBClusterIdentifier")

	input := rdsdriver.ModifyInstanceInput{
		EngineVersion:      form.Get("EngineVersion"),
		MasterUserPassword: form.Get("MasterUserPassword"),
		Tags:               parseRDSTags(form),
	}

	cluster, err := h.db.ModifyCluster(r.Context(), id, input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBClusterResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors deleteDBInstance but operates on clusters.
func (h *Handler) deleteDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBClusterIdentifier")

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
	last.State = rdsdriver.StateDeleting

	if err := h.db.DeleteCluster(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBClusterResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to other lifecycle ops; each needs its own response type.
func (h *Handler) startDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBClusterIdentifier")

	if err := h.db.StartCluster(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	clusters, err := h.db.DescribeClusters(r.Context(), []string{id})
	if err != nil || len(clusters) == 0 {
		writeErr(w, errClusterNotFound(id))
		return
	}

	awsquery.WriteXMLResponse(w, startDBClusterResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(&clusters[0])},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally similar to other lifecycle ops; each needs its own response type.
func (h *Handler) stopDBCluster(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBClusterIdentifier")

	if err := h.db.StopCluster(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	clusters, err := h.db.DescribeClusters(r.Context(), []string{id})
	if err != nil || len(clusters) == 0 {
		writeErr(w, errClusterNotFound(id))
		return
	}

	awsquery.WriteXMLResponse(w, stopDBClusterResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(&clusters[0])},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors createDBClusterSnapshot but operates on instance snapshots.
func (h *Handler) createDBSnapshot(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := rdsdriver.SnapshotConfig{
		ID:         form.Get("DBSnapshotIdentifier"),
		InstanceID: form.Get("DBInstanceIdentifier"),
		Tags:       parseRDSTags(form),
	}

	snap, err := h.db.CreateSnapshot(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbSnapshotResult{DBSnapshot: toSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBSnapshots(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("DBSnapshotIdentifier")
	instance := form.Get("DBInstanceIdentifier")

	var ids []string
	if id != "" {
		ids = []string{id}
	}

	snaps, err := h.db.DescribeSnapshots(r.Context(), ids, instance)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := dbSnapshotsXML{DBSnapshot: make([]dbSnapshotXML, 0, len(snaps))}
	for i := range snaps {
		out.DBSnapshot = append(out.DBSnapshot, toSnapshotXML(&snaps[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBSnapshotsResponse{
		Xmlns:    Namespace,
		Result:   dbSnapshotsResult{DBSnapshots: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBSnapshotIdentifier")

	snaps, err := h.db.DescribeSnapshots(r.Context(), []string{id}, "")
	if err != nil || len(snaps) == 0 {
		writeErr(w, errSnapshotNotFound(id))
		return
	}

	last := snaps[0]

	if err := h.db.DeleteSnapshot(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbSnapshotResult{DBSnapshot: toSnapshotXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) restoreInstanceFromSnapshot(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	input := rdsdriver.RestoreInstanceInput{
		NewInstanceID: form.Get("DBInstanceIdentifier"),
		SnapshotID:    form.Get("DBSnapshotIdentifier"),
		InstanceClass: form.Get("DBInstanceClass"),
		Tags:          parseRDSTags(form),
	}

	inst, err := h.db.RestoreInstanceFromSnapshot(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, restoreDBInstanceFromDBSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors createDBSnapshot but operates on cluster snapshots.
func (h *Handler) createDBClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := rdsdriver.ClusterSnapshotConfig{
		ID:        form.Get("DBClusterSnapshotIdentifier"),
		ClusterID: form.Get("DBClusterIdentifier"),
		Tags:      parseRDSTags(form),
	}

	snap, err := h.db.CreateClusterSnapshot(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBClusterSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbClusterSnapshotResult{DBClusterSnapshot: toClusterSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBClusterSnapshots(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("DBClusterSnapshotIdentifier")
	cluster := form.Get("DBClusterIdentifier")

	var ids []string
	if id != "" {
		ids = []string{id}
	}

	snaps, err := h.db.DescribeClusterSnapshots(r.Context(), ids, cluster)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := dbClusterSnapshotsXML{DBClusterSnapshot: make([]dbClusterSnapshotXML, 0, len(snaps))}
	for i := range snaps {
		out.DBClusterSnapshot = append(out.DBClusterSnapshot, toClusterSnapshotXML(&snaps[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBClusterSnapshotsResponse{
		Xmlns:    Namespace,
		Result:   dbClusterSnapshotsResult{DBClusterSnapshots: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("DBClusterSnapshotIdentifier")

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

	awsquery.WriteXMLResponse(w, deleteDBClusterSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbClusterSnapshotResult{DBClusterSnapshot: toClusterSnapshotXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors createDBSnapshot but operates on cluster restore inputs.
func (h *Handler) restoreClusterFromSnapshot(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	input := rdsdriver.RestoreClusterInput{
		NewClusterID: form.Get("DBClusterIdentifier"),
		SnapshotID:   form.Get("SnapshotIdentifier"),
		Tags:         parseRDSTags(form),
	}

	cluster, err := h.db.RestoreClusterFromSnapshot(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, restoreDBClusterFromSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

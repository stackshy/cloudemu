package rds

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// paginateRDS stable-sorts items by the identifier that key returns, then slices a
// Marker/MaxRecords page out of them — the shared body behind every RDS
// Describe* list handler. On an invalid Marker it writes the RDS
// InvalidParameterValue wire error and returns ok=false so the caller returns
// without emitting a result.
func paginateRDS[T any](w http.ResponseWriter, r *http.Request,
	items []T, key func(*T) string,
) (pagination.Page[T], bool) {
	page, err := pagination.PaginateSorted(items,
		func(a, b T) bool { return key(&a) < key(&b) },
		r.Form.Get("Marker"), formInt(r.Form.Get("MaxRecords")))
	if err != nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "invalid Marker")
		return pagination.Page[T]{}, false
	}

	return page, true
}

// instanceFromForm pulls the common DBInstance fields out of a form. Used by
// CreateDBInstance and as the basis for ModifyDBInstance.
func instanceConfigFromForm(form url.Values) rdsdriver.InstanceConfig {
	return rdsdriver.InstanceConfig{
		ID:                   form.Get("DBInstanceIdentifier"),
		Engine:               form.Get("Engine"),
		EngineVersion:        form.Get("EngineVersion"),
		InstanceClass:        form.Get("DBInstanceClass"),
		AllocatedStorage:     formInt(form.Get("AllocatedStorage")),
		StorageType:          form.Get("StorageType"),
		MasterUsername:       form.Get("MasterUsername"),
		MasterUserPassword:   form.Get("MasterUserPassword"),
		DBName:               form.Get("DBName"),
		Port:                 formInt(form.Get("Port")),
		MultiAZ:              formBool(form.Get("MultiAZ")),
		PubliclyAccessible:   formBool(form.Get("PubliclyAccessible")),
		VPCSecurityGroups:    awsquery.ListStrings(form, "VpcSecurityGroupIds.VpcSecurityGroupId"),
		SubnetGroupName:      form.Get("DBSubnetGroupName"),
		DBParameterGroupName: form.Get("DBParameterGroupName"),
		OptionGroupName:      form.Get("OptionGroupName"),
		ClusterID:            form.Get("DBClusterIdentifier"),
		AvailabilityZone:     form.Get("AvailabilityZone"),
		StorageEncrypted:     formBool(form.Get("StorageEncrypted")),
		KmsKeyID:             form.Get("KmsKeyId"),
		DeletionProtection:   formBool(form.Get("DeletionProtection")),
		Tags:                 parseRDSTags(form),
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
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst, h.resolveInstanceSubnetGroupXML(r.Context(), inst))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

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

	insts = filterInstances(insts, parseInstanceFilters(r.Form))

	page, ok := paginateRDS(w, r, insts, func(i *rdsdriver.Instance) string { return i.ID })
	if !ok {
		return
	}

	out := dbInstancesXML{DBInstance: make([]dbInstanceXML, 0, len(page.Items))}
	for i := range page.Items {
		out.DBInstance = append(out.DBInstance,
			toInstanceXML(&page.Items[i], h.resolveInstanceSubnetGroupXML(r.Context(), &page.Items[i])))
	}

	awsquery.WriteXMLResponse(w, describeDBInstancesResponse{
		Xmlns:    Namespace,
		Result:   dbInstancesResult{Marker: page.NextPageToken, DBInstances: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseInstanceFilters reads the Filters.Filter.N.{Name,Values.Value.M} entries
// into a name→values map. Only the DescribeDBInstances-supported filter names
// are meaningful; unknown names are kept and simply match nothing.
func parseInstanceFilters(form url.Values) map[string][]string {
	indices := awsquery.CollectIndices(form, "Filters.Filter")
	if len(indices) == 0 {
		return nil
	}

	out := make(map[string][]string, len(indices))

	for _, n := range indices {
		base := "Filters.Filter." + strconv.Itoa(n)

		name := form.Get(base + ".Name")
		if name == "" {
			continue
		}

		out[name] = awsquery.ListStrings(form, base+".Values.Value")
	}

	return out
}

// filterInstances keeps only the instances matching every supplied filter (AND
// across names, OR within a name's values), mirroring RDS filter semantics for
// db-instance-id, engine and db-cluster-id.
func filterInstances(insts []rdsdriver.Instance, filters map[string][]string) []rdsdriver.Instance {
	if len(filters) == 0 {
		return insts
	}

	out := make([]rdsdriver.Instance, 0, len(insts))

	for i := range insts {
		if instanceMatchesFilters(&insts[i], filters) {
			out = append(out, insts[i])
		}
	}

	return out
}

func instanceMatchesFilters(inst *rdsdriver.Instance, filters map[string][]string) bool {
	for name, values := range filters {
		var field string

		switch name {
		case "db-instance-id":
			field = inst.ID
		case "engine":
			field = inst.Engine
		case "db-cluster-id":
			field = inst.ClusterID
		default:
			return false
		}

		if !containsString(values, field) {
			return false
		}
	}

	return true
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}

	return false
}

func (h *Handler) modifyDBInstance(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("DBInstanceIdentifier")

	input := rdsdriver.ModifyInstanceInput{
		InstanceClass:              form.Get("DBInstanceClass"),
		AllocatedStorage:           formInt(form.Get("AllocatedStorage")),
		EngineVersion:              form.Get("EngineVersion"),
		MasterUserPassword:         form.Get("MasterUserPassword"),
		DBParameterGroupName:       form.Get("DBParameterGroupName"),
		OptionGroupName:            form.Get("OptionGroupName"),
		NewInstanceID:              form.Get("NewDBInstanceIdentifier"),
		BackupRetentionPeriod:      formInt(form.Get("BackupRetentionPeriod")),
		PreferredBackupWindow:      form.Get("PreferredBackupWindow"),
		PreferredMaintenanceWindow: form.Get("PreferredMaintenanceWindow"),
		StorageType:                form.Get("StorageType"),
		Iops:                       formInt(form.Get("Iops")),
		ApplyImmediately:           formBool(form.Get("ApplyImmediately")),
		Tags:                       parseRDSTags(form),
	}

	if v := form.Get("MultiAZ"); v != "" {
		b := formBool(v)
		input.MultiAZ = &b
	}

	if v := form.Get("DeletionProtection"); v != "" {
		b := formBool(v)
		input.DeletionProtection = &b
	}

	inst, err := h.db.ModifyInstance(r.Context(), id, input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst, h.resolveInstanceSubnetGroupXML(r.Context(), inst))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

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

	// A deletion-protected instance cannot be deleted; the flag must be cleared
	// via ModifyDBInstance first. Real RDS rejects with InvalidParameterCombination
	// and leaves the instance untouched.
	if last.DeletionProtection {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterCombination",
			"Cannot delete protected DB Instance, please disable deletion protection and try again.")

		return
	}

	// A standalone instance takes a final snapshot unless SkipFinalSnapshot is
	// set. When a final snapshot is requested, FinalDBSnapshotIdentifier is
	// mandatory (InvalidParameterCombination otherwise). Cluster members carry no
	// final-snapshot semantics — that belongs to DeleteDBCluster.
	var finalID string

	if last.ClusterID == "" && !formBool(r.Form.Get("SkipFinalSnapshot")) {
		finalID = r.Form.Get("FinalDBSnapshotIdentifier")
		if finalID == "" {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterCombination",
				"FinalDBSnapshotIdentifier is required unless SkipFinalSnapshot is true")

			return
		}

		if _, err := h.db.CreateSnapshot(r.Context(),
			rdsdriver.SnapshotConfig{ID: finalID, InstanceID: id}); err != nil {
			writeErr(w, err)
			return
		}
	}

	if err := h.db.DeleteInstance(r.Context(), id); err != nil {
		// The delete precondition (live read replicas, invalid state) is enforced
		// inside DeleteInstance, so a rejection can land after the final snapshot
		// was already written. Roll that snapshot back so a rejected delete leaves
		// no phantom snapshot behind — real RDS validates before taking it.
		if finalID != "" {
			_ = h.db.DeleteSnapshot(r.Context(), finalID)
		}

		writeErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, deleteDBInstanceResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&last, h.resolveInstanceSubnetGroupXML(r.Context(), &last))},
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
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&insts[0], h.resolveInstanceSubnetGroupXML(r.Context(), &insts[0]))},
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
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&insts[0], h.resolveInstanceSubnetGroupXML(r.Context(), &insts[0]))},
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
		Result:   dbInstanceResult{DBInstance: toInstanceXML(&insts[0], h.resolveInstanceSubnetGroupXML(r.Context(), &insts[0]))},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) createDBCluster(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	cfg := rdsdriver.ClusterConfig{
		ID:                          form.Get("DBClusterIdentifier"),
		Engine:                      form.Get("Engine"),
		EngineVersion:               form.Get("EngineVersion"),
		MasterUsername:              form.Get("MasterUsername"),
		MasterUserPassword:          form.Get("MasterUserPassword"),
		DatabaseName:                form.Get("DatabaseName"),
		Port:                        formInt(form.Get("Port")),
		VPCSecurityGroups:           awsquery.ListStrings(form, "VpcSecurityGroupIds.VpcSecurityGroupId"),
		SubnetGroupName:             form.Get("DBSubnetGroupName"),
		DBClusterParameterGroupName: form.Get("DBClusterParameterGroupName"),
		EngineMode:                  form.Get("EngineMode"),
		StorageEncrypted:            formBool(form.Get("StorageEncrypted")),
		KmsKeyID:                    form.Get("KmsKeyId"),
		AllocatedStorage:            formInt(form.Get("AllocatedStorage")),
		DeletionProtection:          formBool(form.Get("DeletionProtection")),
		Tags:                        parseRDSTags(form),
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

	page, ok := paginateRDS(w, r, clusters, func(c *rdsdriver.Cluster) string { return c.ID })
	if !ok {
		return
	}

	out := dbClustersXML{DBCluster: make([]dbClusterXML, 0, len(page.Items))}
	for i := range page.Items {
		out.DBCluster = append(out.DBCluster, toClusterXML(&page.Items[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBClustersResponse{
		Xmlns:    Namespace,
		Result:   dbClustersResult{Marker: page.NextPageToken, DBClusters: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBCluster(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	id := form.Get("DBClusterIdentifier")

	input := rdsdriver.ModifyInstanceInput{
		EngineVersion:               form.Get("EngineVersion"),
		MasterUserPassword:          form.Get("MasterUserPassword"),
		DBClusterParameterGroupName: form.Get("DBClusterParameterGroupName"),
		Tags:                        parseRDSTags(form),
	}

	if v := form.Get("DeletionProtection"); v != "" {
		b := formBool(v)
		input.DeletionProtection = &b
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

	// A deletion-protected cluster cannot be deleted; the flag must be cleared via
	// ModifyDBCluster first. Real RDS rejects with InvalidParameterCombination.
	if last.DeletionProtection {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterCombination",
			"Cannot delete protected DB Cluster, please disable deletion protection and try again.")

		return
	}

	// A cluster takes a final snapshot unless SkipFinalSnapshot is set. When a
	// final snapshot is requested, FinalDBSnapshotIdentifier is mandatory
	// (InvalidParameterCombination otherwise), mirroring DeleteDBInstance.
	if !formBool(r.Form.Get("SkipFinalSnapshot")) {
		finalID := r.Form.Get("FinalDBSnapshotIdentifier")
		if finalID == "" {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterCombination",
				"FinalDBSnapshotIdentifier is required unless SkipFinalSnapshot is true")

			return
		}

		if _, err := h.db.CreateClusterSnapshot(r.Context(),
			rdsdriver.ClusterSnapshotConfig{ID: finalID, ClusterID: id}); err != nil {
			writeErr(w, err)
			return
		}
	}

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

	// A specific DBSnapshotIdentifier that matches nothing is a hard error in
	// real RDS (DBSnapshotNotFoundFault), mirroring DescribeDBInstances /
	// DescribeDBClusters; the provider's filter-style lookup returns an empty
	// set instead, so surface the fault here.
	if id != "" && len(snaps) == 0 {
		writeErr(w, errSnapshotNotFound(id))
		return
	}

	page, ok := paginateRDS(w, r, snaps, func(s *rdsdriver.Snapshot) string { return s.ID })
	if !ok {
		return
	}

	out := dbSnapshotsXML{DBSnapshot: make([]dbSnapshotXML, 0, len(page.Items))}
	for i := range page.Items {
		out.DBSnapshot = append(out.DBSnapshot, toSnapshotXML(&page.Items[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBSnapshotsResponse{
		Xmlns:    Namespace,
		Result:   dbSnapshotsResult{Marker: page.NextPageToken, DBSnapshots: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // shape mirrors deleteDBClusterSnapshot but operates on instance snapshots.
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
		Port:          formInt(form.Get("Port")),
		Tags:          parseRDSTags(form),
	}

	inst, err := h.db.RestoreInstanceFromSnapshot(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, restoreDBInstanceFromDBSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst, h.resolveInstanceSubnetGroupXML(r.Context(), inst))},
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

//nolint:dupl // shape mirrors deleteDBSnapshot but operates on cluster snapshots.
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

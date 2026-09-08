package dynamodb

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// routeGlobalTables dispatches the version-2017 global-table operations. Without
// them Create/Describe/Update/ListGlobalTables return UnknownOperationException
// and the aws_dynamodb_global_table resource cannot be managed.
func (h *Handler) routeGlobalTables(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateGlobalTable":
		h.createGlobalTable(w, r)
	case "DescribeGlobalTable":
		h.describeGlobalTable(w, r)
	case "UpdateGlobalTable":
		h.updateGlobalTable(w, r)
	case "ListGlobalTables":
		h.listGlobalTables(w, r)
	default:
		return false
	}

	return true
}

// globalTabler returns the driver's GlobalTabler capability, writing an error
// response and returning false when it is unsupported.
func (h *Handler) globalTabler(w http.ResponseWriter) (dbdriver.GlobalTabler, bool) {
	g, ok := h.db.(dbdriver.GlobalTabler)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "global tables are not supported by this driver")

		return nil, false
	}

	return g, true
}

// replicaJSON is a Replica in a create/update replication group.
type replicaJSON struct {
	RegionName string `json:"RegionName"`
}

func (h *Handler) createGlobalTable(w http.ResponseWriter, r *http.Request) {
	g, ok := h.globalTabler(w)
	if !ok {
		return
	}

	var req struct {
		GlobalTableName  string        `json:"GlobalTableName"`
		ReplicationGroup []replicaJSON `json:"ReplicationGroup"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := g.CreateGlobalTable(r.Context(), req.GlobalTableName, regionNames(req.ReplicationGroup))
	if err != nil {
		// A missing underlying table is a TableNotFoundException on create.
		writeGlobalTableErr(w, err, "TableNotFoundException")
		return
	}

	wire.WriteJSON(w, map[string]any{"GlobalTableDescription": globalTableDescription(&info)})
}

func (h *Handler) describeGlobalTable(w http.ResponseWriter, r *http.Request) {
	g, ok := h.globalTabler(w)
	if !ok {
		return
	}

	var req struct {
		GlobalTableName string `json:"GlobalTableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := g.DescribeGlobalTable(r.Context(), req.GlobalTableName)
	if err != nil {
		writeGlobalTableErr(w, err, "GlobalTableNotFoundException")
		return
	}

	wire.WriteJSON(w, map[string]any{"GlobalTableDescription": globalTableDescription(&info)})
}

func (h *Handler) updateGlobalTable(w http.ResponseWriter, r *http.Request) {
	g, ok := h.globalTabler(w)
	if !ok {
		return
	}

	var req struct {
		GlobalTableName string `json:"GlobalTableName"`
		ReplicaUpdates  []struct {
			Create *replicaJSON `json:"Create"`
			Delete *replicaJSON `json:"Delete"`
		} `json:"ReplicaUpdates"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	var add, remove []string

	for _, u := range req.ReplicaUpdates {
		if u.Create != nil {
			add = append(add, u.Create.RegionName)
		}

		if u.Delete != nil {
			remove = append(remove, u.Delete.RegionName)
		}
	}

	info, err := g.UpdateGlobalTable(r.Context(), req.GlobalTableName, add, remove)
	if err != nil {
		writeGlobalTableErr(w, err, "GlobalTableNotFoundException")
		return
	}

	wire.WriteJSON(w, map[string]any{"GlobalTableDescription": globalTableDescription(&info)})
}

func (h *Handler) listGlobalTables(w http.ResponseWriter, r *http.Request) {
	g, ok := h.globalTabler(w)
	if !ok {
		return
	}

	var req struct {
		RegionName string `json:"RegionName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tables, err := g.ListGlobalTables(r.Context(), req.RegionName)
	if err != nil {
		writeGlobalTableErr(w, err, "GlobalTableNotFoundException")
		return
	}

	rendered := make([]map[string]any, 0, len(tables))
	for i := range tables {
		rendered = append(rendered, map[string]any{
			"GlobalTableName":  tables[i].Name,
			"ReplicationGroup": replicaGroup(tables[i].Regions),
		})
	}

	wire.WriteJSON(w, map[string]any{"GlobalTables": rendered})
}

// globalTableDescription renders the GlobalTableDescription block common to the
// create/describe/update responses.
func globalTableDescription(info *dbdriver.GlobalTableInfo) map[string]any {
	return map[string]any{
		"GlobalTableName":   info.Name,
		"GlobalTableArn":    info.Arn,
		"GlobalTableStatus": info.Status,
		"CreationDateTime":  info.CreatedUnix,
		"ReplicationGroup":  replicaGroup(info.Regions),
	}
}

// replicaGroup renders a replica list as ReplicationGroup entries.
func replicaGroup(regions []string) []map[string]any {
	group := make([]map[string]any, 0, len(regions))
	for _, region := range regions {
		group = append(group, map[string]any{"RegionName": region})
	}

	return group
}

// regionNames extracts the region names from a decoded replication group.
func regionNames(replicas []replicaJSON) []string {
	names := make([]string, 0, len(replicas))
	for _, rep := range replicas {
		names = append(names, rep.RegionName)
	}

	return names
}

// writeGlobalTableErr maps a provider error to the DynamoDB global-table
// exception the caller expects. notFoundEx is the exception for a not-found
// (TableNotFoundException on create, GlobalTableNotFoundException elsewhere);
// an already-exists becomes GlobalTableAlreadyExistsException (create) or
// ReplicaAlreadyExistsException (update), and a failed-precondition is a
// missing replica on update (ReplicaNotFoundException).
func writeGlobalTableErr(w http.ResponseWriter, err error, notFoundEx string) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, notFoundEx, errMessage(err))
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, alreadyExistsEx(notFoundEx), errMessage(err))
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ReplicaNotFoundException", errMessage(err))
	default:
		writeErr(w, err)
	}
}

// alreadyExistsEx picks the already-exists exception for a global-table
// operation: create conflicts on the global table itself, every other op's
// conflict is a duplicate replica.
func alreadyExistsEx(notFoundEx string) string {
	if notFoundEx == "TableNotFoundException" {
		return "GlobalTableAlreadyExistsException"
	}

	return "ReplicaAlreadyExistsException"
}

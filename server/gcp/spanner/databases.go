package spanner

import (
	"net/http"
	"strings"

	sp "google.golang.org/api/spanner/v1"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

func toWireDatabase(in *spdriver.Database) *sp.Database {
	return &sp.Database{
		Name:                   in.Name,
		State:                  in.State,
		DatabaseDialect:        in.DatabaseDialect,
		VersionRetentionPeriod: in.VersionRetentionPeriod,
		EnableDropProtection:   in.EnableDropProtection,
		CreateTime:             formatTime(in.CreateTime),
	}
}

// Path-tail lengths after ".../instances/{i}/databases".
const (
	restDatabaseItem = 1 // {d}
	restDatabaseDdl  = 2 // {d}/ddl
	restDatabaseOp   = 3 // {d}/operations/{op}
	idxRestSub       = 1 // rest[1] is the sub-segment (ddl | operations)
)

// serveDatabases routes database, DDL, and database-operation requests. rest is
// the path tail after ".../instances/{i}/databases".
func (h *Handler) serveDatabases(w http.ResponseWriter, r *http.Request, instance string, rest []string) {
	switch {
	case len(rest) == 0:
		h.serveDatabaseCollection(w, r, instance)
	case len(rest) == restDatabaseItem:
		h.serveDatabaseItem(w, r, databaseName(instance, rest[0]))
	case len(rest) == restDatabaseDdl && rest[idxRestSub] == segDdl:
		h.serveDatabaseDdl(w, r, databaseName(instance, rest[0]))
	case len(rest) == restDatabaseOp && rest[idxRestSub] == segOperations:
		h.serveOperation(w, r)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Spanner database path")
	}
}

func databaseName(instance, id string) string {
	return instance + "/databases/" + id
}

func (h *Handler) serveDatabaseCollection(w http.ResponseWriter, r *http.Request, instance string) {
	switch r.Method {
	case http.MethodPost:
		h.createDatabase(w, r, instance)
	case http.MethodGet:
		h.listDatabases(w, r, instance)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request, instance string) {
	var body sp.CreateDatabaseRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	dbID := databaseIDFromStatement(body.CreateStatement)
	if dbID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"createStatement must be of the form CREATE DATABASE `name`")

		return
	}

	db, op, err := h.db.CreateDatabase(r.Context(), spdriver.CreateDatabaseConfig{
		Parent:          instance,
		Name:            databaseName(instance, dbID),
		DatabaseDialect: body.DatabaseDialect,
		ExtraStatements: body.ExtraStatements,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, toWireDatabase(db)))
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request, instance string) {
	dbs, err := h.db.ListDatabases(r.Context(), instance)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, &sp.ListDatabasesResponse{Databases: mapWire(dbs, toWireDatabase)})
}

func (h *Handler) serveDatabaseItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getDatabase(w, r, name)
	case http.MethodDelete:
		h.dropDatabase(w, r, name)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request, name string) {
	db, err := h.db.GetDatabase(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toWireDatabase(db))
}

func (h *Handler) dropDatabase(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.db.DropDatabase(r.Context(), name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) serveDatabaseDdl(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getDatabaseDdl(w, r, name)
	case http.MethodPatch:
		h.updateDatabaseDdl(w, r, name)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) getDatabaseDdl(w http.ResponseWriter, r *http.Request, name string) {
	stmts, err := h.db.GetDatabaseDdl(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, &sp.GetDatabaseDdlResponse{Statements: stmts})
}

func (h *Handler) updateDatabaseDdl(w http.ResponseWriter, r *http.Request, name string) {
	var body sp.UpdateDatabaseDdlRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	db, op, err := h.db.UpdateDatabaseDdl(r.Context(), name, body.Statements)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Real Spanner's updateDdl operation carries a google.protobuf.Empty
	// response, but clients such as the Terraform google provider wait on this
	// operation and require a non-empty `response`; echoing the database keeps
	// that wait from failing with "`resource` not set in operation response".
	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, toWireDatabase(db)))
}

// databaseIDFromStatement extracts the database id from a
// "CREATE DATABASE `name`" statement, tolerating GoogleSQL backticks and
// PostgreSQL double quotes. It returns "" when the statement is not a recognized
// CREATE DATABASE.
func databaseIDFromStatement(stmt string) string {
	fields := strings.Fields(stmt)

	const wantPrefix = 3 // CREATE DATABASE <name>
	if len(fields) < wantPrefix {
		return ""
	}

	if !strings.EqualFold(fields[0], "create") || !strings.EqualFold(fields[1], "database") {
		return ""
	}

	return strings.Trim(fields[2], "`\"")
}

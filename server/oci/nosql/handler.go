// Package nosql implements OCI NoSQL Database Cloud Service's REST API
// against a CloudEmu database driver. Real github.com/oracle/oci-go-sdk nosql
// clients hit this handler the same way they hit
// nosql.<region>.oci.oraclecloud.com.
//
// Supported operations, all under the /20190828 API version:
//
//	POST                 /tables                                  — CreateTable
//	GET                  /tables                                  — ListTables
//	GET/PUT/DELETE       /tables/{tableNameOrId}                  — Get, Update, Delete
//	POST                 /tables/{tableNameOrId}/actions/changeCompartment
//	POST/GET             /tables/{tableNameOrId}/indexes          — CreateIndex, ListIndexes
//	GET/DELETE           /tables/{tableNameOrId}/indexes/{name}   — GetIndex, DeleteIndex
//	GET/PUT/DELETE       /tables/{tableNameOrId}/rows             — GetRow, UpdateRow, DeleteRow
//	POST                 /query                                   — Query
//
// Table and index mutations are asynchronous in real OCI, so they answer 202
// with an opc-work-request-id the shared poller resolves; row writes are
// synchronous and answer 200.
//
// Not emulated, and answered with a 501 naming the gap rather than a bare
// 404: /tables/{id}/usage, which reports throughput consumption CloudEmu does
// not meter, and /query/prepare and /query/summarize, which hand back a
// prepared-statement handle the mock has no plan to bind it to. OCI NoSQL
// publishes no change stream, so there is no DynamoDB-Streams equivalent to
// serve. Tables report ACTIVE from the moment they are created: every
// CloudEmu mutation is synchronous, so the CREATING and DELETING states an
// SDK waiter may poll for are never observable.
package nosql

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	nosqlprovider "github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// apiVersion is the NoSQL Database API version, distinct from every other
// OCI service's.
const apiVersion = "20190828"

// Collections and sub-collections this handler claims.
const (
	segTables  = "tables"
	segQuery   = "query"
	subIndexes = "indexes"
	subRows    = "rows"
	subActions = "actions"
	subUsage   = "usage"

	actionChangeCompartment = "changeCompartment"
	queryPrepare            = "prepare"
	querySummarize          = "summarize"
)

// Work request operation types the asynchronous mutations record.
const (
	opCreateTable       = "CREATE_TABLE"
	opUpdateTable       = "UPDATE_TABLE"
	opDeleteTable       = "DELETE_TABLE"
	opChangeCompartment = "CHANGE_TABLE_COMPARTMENT"
	opCreateIndex       = "CREATE_INDEX"
	opDeleteIndex       = "DELETE_INDEX"
)

// Entity types a work request reports against.
const (
	entityTable = "table"
	entityIndex = "index"
)

// OCI error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
)

// maxPathSegments is /{version}/tables/{id}/{sub}/{name}.
const maxPathSegments = 5

// Extras is the OCI-only surface the portable database driver cannot express:
// tables are created from a DDL statement rather than a key list, carry an
// OCID, a compartment and capacity limits, and rows are addressed by typed
// primary key columns. *providers/oci/nosql.Mock satisfies it; any driver
// that does not is served 501 for every path this handler claims.
type Extras interface {
	CreateOCITable(ctx context.Context, spec nosqlprovider.TableSpec) (*nosqlprovider.Table, error)
	GetOCITable(ctx context.Context, nameOrID string) (*nosqlprovider.Table, error)
	ListOCITables(ctx context.Context, compartmentID, name string) ([]nosqlprovider.Table, error)
	UpdateOCITable(
		ctx context.Context, nameOrID string, upd nosqlprovider.TableUpdate,
	) (*nosqlprovider.Table, error)
	DeleteOCITable(ctx context.Context, nameOrID string) error
	ChangeOCITableCompartment(ctx context.Context, nameOrID, compartmentID string) error

	CreateOCIIndex(
		ctx context.Context, nameOrID string, spec nosqlprovider.IndexSpec, ifNotExists bool,
	) (*nosqlprovider.Index, error)
	GetOCIIndex(ctx context.Context, nameOrID, indexName string) (*nosqlprovider.Index, error)
	ListOCIIndexes(ctx context.Context, nameOrID, indexName string) ([]nosqlprovider.Index, error)
	DeleteOCIIndex(ctx context.Context, nameOrID, indexName string, ifExists bool) error

	GetOCIRow(ctx context.Context, nameOrID string, key map[string]string) (*nosqlprovider.Row, error)
	PutOCIRow(
		ctx context.Context, nameOrID string, value map[string]any, option string,
	) (*nosqlprovider.Row, error)
	DeleteOCIRow(ctx context.Context, nameOrID string, key map[string]string) (bool, error)
	QueryOCI(ctx context.Context, compartmentID, statement string, limit int) ([]map[string]any, error)

	OCITableScope(nameOrID string) string
}

// Handler serves OCI NoSQL Database against a database driver.
type Handler struct {
	extras Extras
	work   *workrequest.Store
}

// New returns a NoSQL handler. work records the asynchronous table and index
// mutations; a nil store leaves those paths unserved.
func New(db dbdriver.Database, work *workrequest.Store) *Handler {
	extras, _ := db.(Extras)

	return &Handler{extras: extras, work: work}
}

// route is a parsed NoSQL path.
type route struct {
	Collection string
	ID         string
	Sub        string
	Name       string
}

// Matches claims /20190828/tables and /20190828/query, and nothing else. The
// shared work request poller keeps /20190828/workRequests, which this handler
// deliberately leaves alone.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rt.Collection == segTables || rt.Collection == segQuery
}

// ServeHTTP routes on collection, then on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed NoSQL path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired database driver does not implement OCI NoSQL tables")

		return
	}

	if rt.Collection == segQuery {
		h.serveQuery(w, r, rt)
		return
	}

	h.serveTables(w, r, rt)
}

// serveTables dispatches the table collection and everything hanging off one
// table.
func (h *Handler) serveTables(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.ID == "" {
		switch r.Method {
		case http.MethodPost:
			h.createTable(w, r)
		case http.MethodGet:
			h.listTables(w, r)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch rt.Sub {
	case "":
		h.serveTable(w, r, rt.ID)
	case subIndexes:
		h.serveIndexes(w, r, rt)
	case subRows:
		h.serveRows(w, r, rt.ID)
	case subActions:
		h.serveTableAction(w, r, rt)
	case subUsage:
		unemulated(w, r, "table usage",
			"CloudEmu does not meter read, write and storage consumption")
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown sub-collection "+rt.Sub)
	}
}

func (h *Handler) serveTable(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getTable(w, r, id)
	case http.MethodPut:
		h.updateTable(w, r, id)
	case http.MethodDelete:
		h.deleteTable(w, r, id)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveTableAction(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	if rt.Name != actionChangeCompartment {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+rt.Name)
		return
	}

	h.changeCompartment(w, r, rt.ID)
}

// unemulated reports a path the handler claims but cannot serve, naming why.
func unemulated(w http.ResponseWriter, r *http.Request, what, why string) {
	ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, what+" is not emulated: "+why)
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed")
}

// accepted records a work request for an asynchronous mutation and answers
// 202, which is what an SDK waiter polls on.
func (h *Handler) accepted(w http.ResponseWriter, r *http.Request, operation, compartmentID string, res workrequest.Resource) {
	id := h.work.Accept(operation, compartmentID, res)

	ocirest.SetWorkRequestID(w, id)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// requireWorkRequests reports whether the asynchronous paths can be served.
func (h *Handler) requireWorkRequests(w http.ResponseWriter, r *http.Request) bool {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return false
	}

	return true
}

// parsePath splits /{version}/{collection}[/{id}[/{sub}[/{name}]]].
func parsePath(urlPath string) (route, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || len(parts) > maxPathSegments || parts[0] != apiVersion {
		return route{}, false
	}

	rt := route{Collection: parts[1]}

	if len(parts) > 2 { //nolint:mnd // the id follows the collection
		rt.ID = parts[2]
	}

	if len(parts) > 3 { //nolint:mnd // then the sub-collection
		rt.Sub = parts[3]
	}

	if len(parts) > 4 { //nolint:mnd // then the name or action within it
		rt.Name = parts[4]
	}

	return rt, true
}

// paginate applies OCI's limit and opaque page cursor, stamping the cursor for
// the next page. The cursor is the offset the next page starts at.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	start := 0

	if token := ocirest.Page(r); token != "" {
		if n, err := strconv.Atoi(token); err == nil && n > 0 {
			start = n
		}
	}

	// items[:0] rather than nil: an empty page is [] on the wire, not null.
	if start >= len(items) {
		return items[:0]
	}

	end := min(start+ocirest.Limit(r), len(items))
	if end < len(items) {
		ocirest.SetNextPage(w, strconv.Itoa(end))
	}

	return items[start:end]
}

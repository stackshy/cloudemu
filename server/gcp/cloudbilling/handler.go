// Package cloudbilling implements the Cloud Billing control plane
// (cloudbilling.googleapis.com v1) and the Cloud Billing Budget API
// (billingbudgets.googleapis.com v1) as a single server.Handler. Real
// google.golang.org/api/cloudbilling/v1 and billingbudgets/v1 clients pointed
// at this server list/get billing accounts, read and update a project's
// billing linkage, browse the service+SKU catalog, and CRUD budgets
// end-to-end.
//
// Scope note: the Cloud Billing API is a control plane. GCP does NOT expose
// per-resource cost or usage figures through it — actual spend lives in the
// BigQuery billing export, not this API — so this handler models accounts,
// project-billing linkage, the catalog, and budgets, not a GetCostAndUsage
// analog. Both APIs are served from one handler because they share the
// /v1/billingAccounts URL space (a budget's name is
// billingAccounts/{id}/budgets/{budgetId}).
//
// Coverage (v1 REST):
//
//	GET    /v1/billingAccounts                                 — billingAccounts.list
//	POST   /v1/billingAccounts                                 — billingAccounts.create
//	GET    /v1/billingAccounts/{id}                            — billingAccounts.get
//	PATCH  /v1/billingAccounts/{id}                            — billingAccounts.patch
//	GET    /v1/billingAccounts/{id}/projects                   — billingAccounts.projects.list
//	GET    /v1/billingAccounts/{id}/budgets                    — budgets.list
//	POST   /v1/billingAccounts/{id}/budgets                    — budgets.create
//	GET    /v1/billingAccounts/{id}/budgets/{budgetId}         — budgets.get
//	PATCH  /v1/billingAccounts/{id}/budgets/{budgetId}         — budgets.patch
//	DELETE /v1/billingAccounts/{id}/budgets/{budgetId}         — budgets.delete
//	GET    /v1/projects/{project}/billingInfo                  — projects.getBillingInfo
//	PUT    /v1/projects/{project}/billingInfo                  — projects.updateBillingInfo
//	GET    /v1/services                                        — services.list
//	GET    /v1/services/{service}/skus                         — services.skus.list
package cloudbilling

import (
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	pathPrefix          = "/v1/"
	billingAccountsSeg  = "billingAccounts"
	projectsSeg         = "projects"
	budgetsSeg          = "budgets"
	billingInfoSeg      = "billingInfo"
	servicesSeg         = "services"
	skusSeg             = "skus"
	billingAccountsName = billingAccountsSeg + "/"
)

// Handler serves the Cloud Billing and Budget REST APIs against an in-memory
// store seeded with a representative billing account and service/SKU catalog.
type Handler struct {
	mu sync.RWMutex
	// accounts is keyed by billing-account id (the {id} in billingAccounts/{id}).
	accounts map[string]*billingAccount
	// projectInfo is keyed by project id; a project absent from the map has no
	// billing linkage yet (getBillingInfo returns a disabled default).
	projectInfo map[string]*projectBillingInfo
	// budgets is keyed by "{accountID}/{budgetID}".
	budgets map[string]*budget
	// services and skus are the static catalog; skus is keyed by service id.
	services []*service
	skus     map[string][]*sku
	// accountSeq numbers generated billing-account ids from billingAccounts.create.
	accountSeq uint64
}

// New returns a Cloud Billing handler seeded with one open billing account and
// a small service/SKU catalog.
func New() *Handler {
	h := &Handler{
		accounts:    map[string]*billingAccount{},
		projectInfo: map[string]*projectBillingInfo{},
		budgets:     map[string]*budget{},
		skus:        map[string][]*sku{},
	}
	h.seed()

	return h
}

// route is a parsed Cloud Billing / Budget v1 path.
type route struct {
	kind      routeKind
	accountID string
	project   string
	serviceID string
	budgetID  string
}

type routeKind int

const (
	kindNone routeKind = iota
	kindAccounts
	kindAccount
	kindAccountProjects
	kindBudgets
	kindBudget
	kindProjectBillingInfo
	kindServices
	kindSkus
)

// parseRoute maps a URL path onto a route. ok is false for any path outside the
// billing/budget surface.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(urlPath, pathPrefix), "/"), "/")

	switch parts[0] {
	case billingAccountsSeg:
		return parseAccountRoute(parts)
	case projectsSeg:
		return parseProjectRoute(parts)
	case servicesSeg:
		return parseServicesRoute(parts)
	default:
		return route{}, false
	}
}

// parseAccountRoute handles /v1/billingAccounts[/{id}[/projects|/budgets[/{budgetId}]]].
func parseAccountRoute(parts []string) (route, bool) {
	const (
		collection = 1 // [billingAccounts]
		resource   = 2 // [billingAccounts, {id}]
		subColl    = 3 // [billingAccounts, {id}, projects|budgets]
		subRes     = 4 // [billingAccounts, {id}, budgets, {budgetId}]
	)

	switch len(parts) {
	case collection:
		return route{kind: kindAccounts}, true
	case resource:
		return route{kind: kindAccount, accountID: parts[1]}, true
	case subColl:
		return parseAccountSubCollection(parts)
	case subRes:
		if parts[2] != budgetsSeg {
			return route{}, false
		}

		return route{kind: kindBudget, accountID: parts[1], budgetID: parts[3]}, true
	default:
		return route{}, false
	}
}

func parseAccountSubCollection(parts []string) (route, bool) {
	switch parts[2] {
	case projectsSeg:
		return route{kind: kindAccountProjects, accountID: parts[1]}, true
	case budgetsSeg:
		return route{kind: kindBudgets, accountID: parts[1]}, true
	default:
		return route{}, false
	}
}

// parseProjectRoute handles /v1/projects/{project}/billingInfo.
func parseProjectRoute(parts []string) (route, bool) {
	const projectInfoParts = 3 // [projects, {project}, billingInfo]
	if len(parts) != projectInfoParts || parts[2] != billingInfoSeg {
		return route{}, false
	}

	return route{kind: kindProjectBillingInfo, project: parts[1]}, true
}

// parseServicesRoute handles /v1/services and /v1/services/{service}/skus.
func parseServicesRoute(parts []string) (route, bool) {
	const (
		collection = 1 // [services]
		skusColl   = 3 // [services, {service}, skus]
	)

	switch len(parts) {
	case collection:
		return route{kind: kindServices}, true
	case skusColl:
		if parts[2] != skusSeg {
			return route{}, false
		}

		return route{kind: kindSkus, serviceID: parts[1]}, true
	default:
		return route{}, false
	}
}

// Matches claims the Cloud Billing / Budget paths. The /v1/projects/{p}/
// billingInfo shape overlaps the /v1/projects/ family (Firestore, IAM, …), so
// this handler must register before them; its billingInfo-suffix guard keeps it
// disjoint from their paths.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

// ServeHTTP dispatches on the parsed route.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A failed parse yields the zero route (kindNone), handled below; ServeHTTP is
	// only reached after Matches so that case is unreachable in practice. Every
	// routeKind is enumerated (no default) so the exhaustive linter guards against
	// an unhandled route.
	rt, _ := parseRoute(r.URL.Path)

	switch rt.kind {
	case kindNone:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Billing path")
	case kindAccounts:
		h.serveAccountCollection(w, r)
	case kindAccount:
		h.serveAccount(w, r, rt)
	case kindAccountProjects:
		h.serveAccountProjects(w, r, rt)
	case kindBudgets:
		h.serveBudgetCollection(w, r, rt)
	case kindBudget:
		h.serveBudget(w, r, rt)
	case kindProjectBillingInfo:
		h.serveProjectBillingInfo(w, r, rt)
	case kindServices:
		h.serveServices(w, r)
	case kindSkus:
		h.serveSkus(w, r, rt)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "unsupported Cloud Billing method")
}

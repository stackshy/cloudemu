package opensearch

import (
	"net/http"
)

// serveOpenSearch routes the /2021-01-01/opensearch/* subtree, which holds the
// bulk of the API. rest is the path below "opensearch".
//
//nolint:gocyclo // one dispatch arm per collection root; the surface is large by design.
func (h *Handler) serveOpenSearch(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch rest[0] {
	case "domain":
		h.serveDomain(w, r, rest[1:])
	case "domain-info":
		h.describeDomains(w, r)
	case "compatibleVersions":
		h.getCompatibleVersions(w, r)
	case "versions":
		h.listVersions(w, r)
	case "instanceTypeLimits":
		h.describeInstanceTypeLimits(w, r, rest[1:])
	case "instanceTypeDetails":
		h.listInstanceTypeDetails(w, r, rest[1:])
	case "serviceSoftwareUpdate":
		h.serveServiceSoftware(w, r, rest[1:])
	case "upgradeDomain":
		h.serveUpgradeDomain(w, r, rest[1:])
	case segVpcEndpoints:
		h.serveVpcEndpoints(w, r, rest[1:])
	case "cc":
		h.serveCrossCluster(w, r, rest[1:])
	case "directQueryDataSource":
		h.serveDirectQuery(w, r, rest[1:])
	case "application":
		h.serveApplication(w, r, rest[1:])
	case "list-applications":
		h.listApplications(w, r)
	case "defaultApplicationSetting":
		h.serveDefaultAppSetting(w, r)
	case "reservedInstances":
		h.describeReservedInstances(w, r)
	case "reservedInstanceOfferings":
		h.describeReservedInstanceOfferings(w, r)
	case "purchaseReservedInstanceOffering":
		h.purchaseReservedInstanceOffering(w, r)
	case "app-migrations":
		h.serveMigrations(w, r, rest[1:])
	case "insights":
		h.listInsights(w, r)
	case "insight-details":
		h.describeInsightDetails(w, r)
	case "insight-feedback":
		h.insightFeedback(w, r)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// Package route53 implements the AWS Route 53 REST+XML protocol as a
// server.Handler. Point the real aws-sdk-go-v2 Route 53 client at a Server
// registered with this handler and hosted-zone and record operations work
// against the shared dns driver.
//
// Route 53 is a REST/XML service rooted at /2013-04-01/hostedzone (unlike the
// JSON-RPC and query-protocol AWS services). Its own path space is disjoint
// from every other AWS handler, but it must register before the permissive S3
// REST fallback so its URLs aren't swallowed.
//
// Coverage (2013-04-01 REST):
//
//	POST   /2013-04-01/hostedzone                    — CreateHostedZone
//	GET    /2013-04-01/hostedzone/{id}               — GetHostedZone
//	GET    /2013-04-01/hostedzone                    — ListHostedZones
//	DELETE /2013-04-01/hostedzone/{id}               — DeleteHostedZone
//	POST   /2013-04-01/hostedzone/{id}/rrset         — ChangeResourceRecordSets (CREATE/UPSERT/DELETE)
//	GET    /2013-04-01/hostedzone/{id}/rrset         — ListResourceRecordSets
package route53

import (
	"net/http"
	"strings"

	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// pathPrefix roots every Route 53 REST URL. The version segment is fixed.
const pathPrefix = "/2013-04-01/hostedzone"

// tagsPrefix roots the Route 53 tagging API: /2013-04-01/tags/{type}/{id}.
const tagsPrefix = "/2013-04-01/tags/"

const rrsetSeg = "rrset"

// Sub-resource path segments under /hostedzone/{id}.
const (
	associateVPCSeg    = "associatevpc"
	disassociateVPCSeg = "disassociatevpc"
)

// Additional Route 53 REST roots handled by this dispatcher.
const (
	changePrefix          = "/2013-04-01/change/"
	hostedZoneCountPath   = "/2013-04-01/hostedzonecount"
	hostedZonesByNamePath = "/2013-04-01/hostedzonesbyname"
	hostedZonesByVPCPath  = "/2013-04-01/hostedzonesbyvpc"
	testDNSAnswerPath     = "/2013-04-01/testdnsanswer"
)

// Handler serves Route 53 REST requests against a dns driver.
type Handler struct {
	dns dnsdriver.DNS
}

// New returns a Route 53 handler backed by d.
func New(d dnsdriver.DNS) *Handler {
	return &Handler{dns: d}
}

// Matches claims /2013-04-01/hostedzone[...] requests — Route 53's own REST
// path space, disjoint from every other AWS handler. Registered before the S3
// REST fallback so those paths aren't swallowed by the catch-all.
func (*Handler) Matches(r *http.Request) bool {
	return r.URL.Path == pathPrefix ||
		strings.HasPrefix(r.URL.Path, pathPrefix+"/") ||
		r.URL.Path == healthCheckPrefix ||
		strings.HasPrefix(r.URL.Path, healthCheckPrefix+"/") ||
		strings.HasPrefix(r.URL.Path, tagsPrefix) ||
		strings.HasPrefix(r.URL.Path, changePrefix) ||
		r.URL.Path == hostedZoneCountPath ||
		r.URL.Path == hostedZonesByNamePath ||
		r.URL.Path == hostedZonesByVPCPath ||
		r.URL.Path == testDNSAnswerPath
}

// ServeHTTP routes on the path tail and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, tagsPrefix) {
		h.serveTags(w, r, strings.TrimPrefix(r.URL.Path, tagsPrefix))
		return
	}

	if r.URL.Path == healthCheckPrefix || strings.HasPrefix(r.URL.Path, healthCheckPrefix+"/") {
		h.serveHealthCheck(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, changePrefix) {
		h.getChange(w, r, strings.TrimPrefix(r.URL.Path, changePrefix))
		return
	}

	switch r.URL.Path {
	case hostedZoneCountPath:
		h.getHostedZoneCount(w, r)
		return
	case hostedZonesByNamePath:
		h.listHostedZonesByName(w, r)
		return
	case hostedZonesByVPCPath:
		h.listHostedZonesByVPC(w, r)
		return
	case testDNSAnswerPath:
		h.testDNSAnswer(w, r)
		return
	}

	h.serveHostedZonePath(w, r)
}

// serveHostedZonePath dispatches the /hostedzone[/{id}[/sub]] path space:
// the collection, a single zone, and the rrset / associatevpc / disassociatevpc
// sub-resources.
func (h *Handler) serveHostedZonePath(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")
	if tail == "" {
		h.serveZoneCollection(w, r)
		return
	}

	// tail is "{id}" or "{id}/{sub}".
	id, sub, _ := strings.Cut(tail, "/")

	switch sub {
	case "":
		h.serveZone(w, r, id)
	case rrsetSeg:
		h.serveRRSet(w, r, id)
	case associateVPCSeg:
		h.serveAssociateVPC(w, r, id)
	case disassociateVPCSeg:
		h.serveDisassociateVPC(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "NoSuchHostedZone", "unrecognized Route 53 path")
	}
}

// serveAssociateVPC dispatches POST /hostedzone/{id}/associatevpc.
func (h *Handler) serveAssociateVPC(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.associateVPCWithHostedZone(w, r, id)
}

// serveDisassociateVPC dispatches POST /hostedzone/{id}/disassociatevpc.
func (h *Handler) serveDisassociateVPC(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.disassociateVPCFromHostedZone(w, r, id)
}

// serveZoneCollection dispatches /hostedzone collection requests.
func (h *Handler) serveZoneCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createHostedZone(w, r)
	case http.MethodGet:
		h.listHostedZones(w, r)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveZone dispatches /hostedzone/{id} resource requests.
func (h *Handler) serveZone(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getHostedZone(w, r, id)
	case http.MethodDelete:
		h.deleteHostedZone(w, r, id)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveRRSet dispatches /hostedzone/{id}/rrset requests.
func (h *Handler) serveRRSet(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPost:
		h.changeResourceRecordSets(w, r, id)
	case http.MethodGet:
		h.listResourceRecordSets(w, r, id)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "InvalidInput", "method not allowed")
}

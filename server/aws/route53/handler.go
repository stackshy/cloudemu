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
//	POST   /2013-04-01/hostedzone                      CreateHostedZone
//	GET    /2013-04-01/hostedzone/{id}                 GetHostedZone
//	GET    /2013-04-01/hostedzone                      ListHostedZones
//	DELETE /2013-04-01/hostedzone/{id}                 DeleteHostedZone
//	POST   /2013-04-01/hostedzone/{id}/rrset           ChangeResourceRecordSets (CREATE/UPSERT/DELETE)
//	GET    /2013-04-01/hostedzone/{id}/rrset           ListResourceRecordSets
package route53

import (
	"net/http"
	"strings"
	"sync"

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

	// zoneMu serializes the two operations that read a zone's record-set state
	// and then act on it in a separate step: ChangeResourceRecordSets (validate
	// the whole batch against current records, then apply every change) and
	// DeleteHostedZone (check the zone holds only the apex SOA/NS, then delete
	// it). The underlying memstore only guarantees each individual Get/Set/
	// Delete call is atomic, not a read-then-act sequence spanning several
	// calls. Without this lock, two concurrent requests against the same zone
	// could interleave between the check and the mutation (e.g. a batch
	// validated as safe gets partially applied around a concurrent DELETE, or
	// DeleteHostedZone deletes a zone a concurrent ChangeResourceRecordSets just
	// added a record to). cloudemu's Route 53 handler is not on a hot path, so a
	// single handler-wide lock is preferred over a per-zone lock map and its
	// bookkeeping.
	zoneMu sync.Mutex
}

// New returns a Route 53 handler backed by d.
func New(d dnsdriver.DNS) *Handler {
	return &Handler{dns: d}
}

// Matches claims /2013-04-01/hostedzone[...] requests, Route 53's own REST
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

// ServeHTTP runs the operation classify names, or writes the error of a
// request it cannot name.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op, a := classify(r)

	if !h.serveZoneOp(w, r, op, &a) && !h.serveOtherOp(w, r, op, &a) {
		h.writeUnknown(w, &a)
	}
}

// serveZoneOp runs op when it is a hosted zone operation.
func (h *Handler) serveZoneOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opCreateHostedZone:
		h.createHostedZone(w, r)
	case opListHostedZones:
		h.listHostedZones(w, r)
	case opGetHostedZone:
		h.getHostedZone(w, r, a.id)
	case opDeleteHostedZone:
		h.deleteHostedZone(w, r, a.id)
	case opUpdateHostedZoneComment:
		h.updateHostedZoneComment(w, r, a.id)
	case opChangeResourceRecordSets:
		h.changeResourceRecordSets(w, r, a.id)
	case opListResourceRecordSets:
		h.listResourceRecordSets(w, r, a.id)
	case opAssociateVPCWithHostedZone:
		h.associateVPCWithHostedZone(w, r, a.id)
	case opDisassociateVPCFromHostedZone:
		h.disassociateVPCFromHostedZone(w, r, a.id)
	default:
		return false
	}

	return true
}

// serveOtherOp runs op when it is a health check, change, tagging or
// account-level operation.
func (h *Handler) serveOtherOp(w http.ResponseWriter, r *http.Request, op opID, a *opArgs) bool {
	switch op {
	case opGetChange:
		h.getChange(w, a.id)
	case opGetHostedZoneCount, opListHostedZonesByName, opListHostedZonesByVPC, opTestDNSAnswer:
		h.serveAccountOp(w, r, op)
	case opCreateHealthCheck:
		h.createHealthCheck(w, r)
	case opListHealthChecks:
		h.listHealthChecks(w, r)
	case opGetHealthCheck:
		h.getHealthCheck(w, r, a.id)
	case opUpdateHealthCheck:
		h.updateHealthCheck(w, r, a.id)
	case opDeleteHealthCheck:
		h.deleteHealthCheck(w, r, a.id)
	case opChangeTagsForResource, opListTagsForResource:
		h.serveTags(w, r, op, a)
	default:
		return false
	}

	return true
}

// serveAccountOp runs the read-only operations that act on no single
// resource.
func (h *Handler) serveAccountOp(w http.ResponseWriter, r *http.Request, op opID) {
	switch op {
	case opGetHostedZoneCount:
		h.getHostedZoneCount(w, r)
	case opListHostedZonesByName:
		h.listHostedZonesByName(w, r)
	case opListHostedZonesByVPC:
		h.listHostedZonesByVPC(w, r)
	default:
		h.testDNSAnswer(w, r)
	}
}

// writeUnknown writes the error of a request classify cannot name. None of
// these branches reads or changes state.
func (h *Handler) writeUnknown(w http.ResponseWriter, a *opArgs) {
	switch a.fail {
	case failPath:
		writeError(w, http.StatusNotFound, "NoSuchHostedZone", "unrecognized Route 53 path")
	case failTags:
		h.writeTagsFailure(w, a)
	case failTagType:
		writeError(w, http.StatusBadRequest, "InvalidInput",
			"Value '"+a.tagType+"' at 'resourceType' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [healthcheck, hostedzone]")
	case failMethod:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "InvalidInput", "method not allowed")
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return serviceName }

// WriteAccessDenied writes the 403 this service returns when IAM denies a
// call, in its own XML error shape.
func (*Handler) WriteAccessDenied(w http.ResponseWriter, _ *http.Request, msg string) {
	writeError(w, http.StatusForbidden, "AccessDenied", msg)
}

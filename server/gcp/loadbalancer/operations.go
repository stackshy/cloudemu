package loadbalancer

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// --- backend services (target groups) ---

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertBackendService(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var req backendServiceRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}

	if _, err := h.findTGByName(r.Context(), req.Name); conflictIfExists(w, err, "backend service "+req.Name+" already exists") {
		return
	}

	tags := backendServiceTags(&req)
	tags[bsCreationTag] = time.Now().UTC().Format(time.RFC3339)

	if _, err := h.lb.CreateTargetGroup(r.Context(), lbdriver.TargetGroupConfig{
		Name:     req.Name,
		Protocol: req.Protocol,
		Port:     req.Port,
		// The driver TargetGroup can't hold these GCP fields, so round-trip them
		// through tags rather than dropping them on read.
		Tags: tags,
	}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceBackendServices, req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// patchBackendService applies compute.backendServices.patch/update: it merges
// the request's non-empty fields onto the existing backend service and returns
// a DONE Operation. Without it, Terraform's google_compute_backend_service —
// which patches on every change — leaves the resource read-only after create.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchBackendService(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	patcher, ok := h.lb.(lbdriver.GCPBackendServicePatcher)
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver cannot patch backend services")
		return
	}

	var req backendServiceRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	err := patcher.PatchGCPBackendService(r.Context(), rp.ResourceName, func(tg *lbdriver.TargetGroupInfo) {
		if req.Protocol != "" {
			tg.Protocol = req.Protocol
		}

		if req.Port != 0 {
			tg.Port = req.Port
		}

		if tg.Tags == nil {
			tg.Tags = map[string]string{}
		}

		mergeBackendServiceTags(tg.Tags, &req)
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceBackendServices, rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getBackendService(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	tg, err := h.findTGByName(r.Context(), rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toBackendServiceResponse(tg, rp, hostOf(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listBackendServices(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	tgs, err := h.lb.DescribeTargetGroups(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	out := backendServiceListResponse{
		Kind:     "compute#backendServiceList",
		ID:       "projects/" + rp.Project + "/global/backendServices",
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceBackendServices, ""),
	}

	for i := range tgs {
		out.Items = append(out.Items, toBackendServiceResponse(&tgs[i], rp, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD delete shape is duplicate-by-design across resource types
func (h *Handler) deleteBackendService(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	tg, err := h.findTGByName(r.Context(), rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.lb.DeleteTargetGroup(r.Context(), tg.ARN); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceBackendServices, rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// --- forwarding rules (load balancers) ---

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var req forwardingRuleRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}

	if _, err := h.findLBByName(r.Context(), req.Name); conflictIfExists(w, err, "forwarding rule "+req.Name+" already exists") {
		return
	}

	lb, err := h.lb.CreateLoadBalancer(r.Context(), lbdriver.LBConfig{
		Name:   req.Name,
		Type:   "network",
		Scheme: schemeFromRule(&req),
		// Round-trip the GCP forwarding-rule fields the driver LB can't model
		// (exact scheme, portRange, IPProtocol, IPAddress, …) through tags.
		Tags: forwardingRuleTags(&req),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// A forwarding rule that references a backend service becomes a listener
	// linking the load balancer to that target group. A dangling reference to a
	// non-existent backend service is an error (as in real GCP), and a failed
	// link must not be swallowed into a phantom success.
	if bsName := backendServiceName(req.BackendService); bsName != "" {
		tg, ferr := h.findTGByName(r.Context(), bsName)
		if ferr != nil {
			gcprest.WriteCErr(w, ferr)
			return
		}

		if _, lerr := h.lb.CreateListener(r.Context(), lbdriver.ListenerConfig{
			LBARN:          lb.ARN,
			Protocol:       req.IPProtocol,
			Port:           firstPort(req.PortRange),
			TargetGroupARN: tg.ARN,
		}); lerr != nil {
			gcprest.WriteCErr(w, lerr)
			return
		}
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceForwardingRules, req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	lb, err := h.findLBByName(r.Context(), rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.toForwardingRuleResponse(r.Context(), lb, rp, hostOf(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listForwardingRules(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	lbs, err := h.lb.DescribeLoadBalancers(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	out := forwardingRuleListResponse{
		Kind:     "compute#forwardingRuleList",
		ID:       "projects/" + rp.Project + "/global/forwardingRules",
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceForwardingRules, ""),
	}

	for i := range lbs {
		out.Items = append(out.Items, h.toForwardingRuleResponse(r.Context(), &lbs[i], rp, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD delete shape is duplicate-by-design across resource types
func (h *Handler) deleteForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	lb, err := h.findLBByName(r.Context(), rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.lb.DeleteLoadBalancer(r.Context(), lb.ARN); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceForwardingRules, rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// --- lookups + response shaping ---

func (h *Handler) findTGByName(ctx context.Context, name string) (*lbdriver.TargetGroupInfo, error) {
	tgs, err := h.lb.DescribeTargetGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range tgs {
		if tgs[i].Name == name {
			return &tgs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "backend service %q not found", name)
}

func (h *Handler) findLBByName(ctx context.Context, name string) (*lbdriver.LBInfo, error) {
	lbs, err := h.lb.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range lbs {
		if lbs[i].Name == name {
			return &lbs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "forwarding rule %q not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toBackendServiceResponse(tg *lbdriver.TargetGroupInfo, rp gcprest.ResourcePath, host string) backendServiceResponse {
	resp := backendServiceResponse{
		Kind:     "compute#backendService",
		ID:       numericID(tg.ID),
		Name:     tg.Name,
		Protocol: tg.Protocol,
		Port:     tg.Port,
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceBackendServices, tg.Name),
	}

	resp.Description = tg.Tags[bsDescriptionTag]
	resp.PortName = tg.Tags[bsPortNameTag]
	resp.LoadBalancingScheme = tg.Tags[bsSchemeTag]
	resp.SessionAffinity = tg.Tags[bsSessionAffinityTag]
	resp.CreationTimestamp = tg.Tags[bsCreationTag]
	// A non-empty fingerprint is required for every future patch; real GCP always
	// returns one, so derive a stable value from the resource name.
	resp.Fingerprint = fingerprintOf(tg.Name)

	if ts := tg.Tags[bsTimeoutSecTag]; ts != "" {
		if n, err := strconv.Atoi(ts); err == nil {
			resp.TimeoutSec = n
		}
	}

	if hc := tg.Tags[bsHealthChecksTag]; hc != "" {
		resp.HealthChecks = strings.Split(hc, ",")
	}

	return resp
}

// Reserved tag keys carry the GCP backend-service fields the driver's target
// group can't model.
const (
	bsDescriptionTag     = "cloudemu:gcpBsDescription"
	bsPortNameTag        = "cloudemu:gcpBsPortName"
	bsHealthChecksTag    = "cloudemu:gcpBsHealthChecks"
	bsSchemeTag          = "cloudemu:gcpBsScheme"
	bsSessionAffinityTag = "cloudemu:gcpBsSessionAffinity"
	bsTimeoutSecTag      = "cloudemu:gcpBsTimeoutSec"
	bsCreationTag        = "cloudemu:gcpBsCreationTimestamp"
)

// backendServiceTags folds the GCP-specific backend-service fields into a fresh
// tag map so they round-trip through the driver.
func backendServiceTags(req *backendServiceRequest) map[string]string {
	tags := map[string]string{}
	mergeBackendServiceTags(tags, req)

	return tags
}

// mergeBackendServiceTags sets a tag for each non-empty field of req, leaving
// tags for omitted fields untouched (patch-merge semantics).
func mergeBackendServiceTags(tags map[string]string, req *backendServiceRequest) {
	if req.Description != "" {
		tags[bsDescriptionTag] = req.Description
	}

	if req.PortName != "" {
		tags[bsPortNameTag] = req.PortName
	}

	if len(req.HealthChecks) > 0 {
		tags[bsHealthChecksTag] = strings.Join(req.HealthChecks, ",")
	}

	if req.LoadBalancingScheme != "" {
		tags[bsSchemeTag] = req.LoadBalancingScheme
	}

	if req.SessionAffinity != "" {
		tags[bsSessionAffinityTag] = req.SessionAffinity
	}

	if req.TimeoutSec != 0 {
		tags[bsTimeoutSecTag] = strconv.Itoa(req.TimeoutSec)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) toForwardingRuleResponse(ctx context.Context, lb *lbdriver.LBInfo,
	rp gcprest.ResourcePath, host string,
) forwardingRuleResponse {
	out := forwardingRuleResponse{
		Kind:                "compute#forwardingRule",
		ID:                  numericID(lb.ID),
		Name:                lb.Name,
		IPAddress:           forwardingRuleIP(lb),
		IPProtocol:          tagOrDefault(lb.Tags, frProtocolTag, "TCP"),
		PortRange:           lb.Tags[frPortRangeTag],
		Description:         lb.Tags[frDescriptionTag],
		LoadBalancingScheme: forwardingRuleScheme(lb),
		CreationTimestamp:   lb.Tags[frCreationTag],
		SelfLink:            gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceForwardingRules, lb.Name),
	}

	// A linked listener (a rule referencing a backend service) supersedes the
	// round-tripped protocol/portRange and adds the backendService self-link.
	if listeners, err := h.lb.DescribeListeners(ctx, lb.ARN); err == nil && len(listeners) > 0 {
		out.IPProtocol = protocolOrDefault(listeners[0].Protocol)
		out.PortRange = strconv.Itoa(listeners[0].Port)

		if tgName := h.tgNameByARN(ctx, listeners[0].TargetGroupARN); tgName != "" {
			out.BackendService = gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "",
				resourceBackendServices, tgName)
		}
	}

	return out
}

// tgNameByARN resolves a target-group ARN back to its name for response links.
func (h *Handler) tgNameByARN(ctx context.Context, arn string) string {
	if arn == "" {
		return ""
	}

	tgs, err := h.lb.DescribeTargetGroups(ctx, []string{arn})
	if err != nil || len(tgs) == 0 {
		return ""
	}

	return tgs[0].Name
}

// backendServiceName extracts the trailing backend-service name from a compute
// self-link or relative reference.
func backendServiceName(ref string) string {
	if ref == "" {
		return ""
	}

	const marker = "/backendServices/"

	if idx := strings.LastIndex(ref, marker); idx >= 0 {
		return ref[idx+len(marker):]
	}

	return ref
}

// firstPort parses the low end of a GCP portRange (e.g. "80" or "80-80").
func firstPort(portRange string) int {
	if portRange == "" {
		return 0
	}

	if idx := strings.Index(portRange, "-"); idx >= 0 {
		portRange = portRange[:idx]
	}

	n, err := strconv.Atoi(strings.TrimSpace(portRange))
	if err != nil {
		return 0
	}

	return n
}

// driverSchemeInternal is the driver-side scheme value for internal LBs.
const driverSchemeInternal = "internal"

// schemeFromRule maps a GCP loadBalancingScheme to the driver scheme.
func schemeFromRule(req *forwardingRuleRequest) string {
	if strings.EqualFold(req.LoadBalancingScheme, "INTERNAL") {
		return driverSchemeInternal
	}

	return "internet-facing"
}

// schemeToGCP maps the driver scheme back to a GCP loadBalancingScheme.
func schemeToGCP(scheme string) string {
	if scheme == driverSchemeInternal {
		return "INTERNAL"
	}

	return "EXTERNAL"
}

// protocolOrDefault normalizes a stored listener protocol, defaulting to TCP.
func protocolOrDefault(p string) string {
	if p == "" {
		return "TCP"
	}

	return p
}

// fnvHash is a 64-bit FNV-1a hash used to derive stable synthetic identity
// (numeric IDs, fingerprints, IP addresses) from a resource's name/ID.
func fnvHash(s string) uint64 {
	const fnvOffset uint64 = 14695981039346656037

	const fnvPrime uint64 = 1099511628211

	h := fnvOffset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime
	}

	return h
}

// numericID returns a stable uint64-shaped string derived from a driver ID.
// GCP wire IDs are uint64 and proto JSON unmarshalling rejects anything else.
func numericID(driverID string) string {
	return strconv.FormatUint(fnvHash(driverID), 10)
}

// fingerprintOf returns a stable base64 fingerprint for a resource. GCP returns
// a fingerprint on every resource and requires it on patch; a stable value is
// enough for a read-modify-write client since the mock does not enforce it.
func fingerprintOf(name string) string {
	var b [8]byte

	binary.BigEndian.PutUint64(b[:], fnvHash("fp:"+name))

	return base64.StdEncoding.EncodeToString(b[:])
}

// Reserved tag keys carry the GCP forwarding-rule fields the driver's load
// balancer can't model.
const (
	frPortRangeTag   = "cloudemu:gcpFrPortRange"
	frProtocolTag    = "cloudemu:gcpFrIPProtocol"
	frSchemeTag      = "cloudemu:gcpFrScheme"
	frIPAddressTag   = "cloudemu:gcpFrIPAddress"
	frDescriptionTag = "cloudemu:gcpFrDescription"
	frCreationTag    = "cloudemu:gcpFrCreationTimestamp"
)

// forwardingRuleTags folds the GCP-specific forwarding-rule fields into a tag
// map so they round-trip through the driver's LB record.
func forwardingRuleTags(req *forwardingRuleRequest) map[string]string {
	tags := map[string]string{
		frCreationTag: time.Now().UTC().Format(time.RFC3339),
	}

	if req.PortRange != "" {
		tags[frPortRangeTag] = req.PortRange
	}

	if req.IPProtocol != "" {
		tags[frProtocolTag] = req.IPProtocol
	}

	if req.LoadBalancingScheme != "" {
		tags[frSchemeTag] = req.LoadBalancingScheme
	}

	if req.IPAddress != "" {
		tags[frIPAddressTag] = req.IPAddress
	}

	if req.Description != "" {
		tags[frDescriptionTag] = req.Description
	}

	return tags
}

// forwardingRuleIP returns the rule's IP address: the client-supplied one when
// present, otherwise a stable synthetic IPv4 (real GCP returns an IP, never a
// hostname).
func forwardingRuleIP(lb *lbdriver.LBInfo) string {
	if ip := lb.Tags[frIPAddressTag]; ip != "" {
		return ip
	}

	// Derive a deterministic public-looking IPv4 from the LB identity.
	h := fnvHash("ip:" + lb.ID + lb.Name)

	const octetMod = 254

	o2 := byte(h%octetMod) + 1
	o3 := byte((h>>8)%octetMod) + 1
	o4 := byte((h>>16)%octetMod) + 1

	return "34." + strconv.Itoa(int(o2)) + "." + strconv.Itoa(int(o3)) + "." + strconv.Itoa(int(o4))
}

// forwardingRuleScheme returns the exact GCP loadBalancingScheme, preferring the
// round-tripped value (EXTERNAL_MANAGED / INTERNAL_MANAGED / …) over the driver
// scheme's lossy EXTERNAL/INTERNAL collapse.
func forwardingRuleScheme(lb *lbdriver.LBInfo) string {
	if s := lb.Tags[frSchemeTag]; s != "" {
		return s
	}

	return schemeToGCP(lb.Scheme)
}

// tagOrDefault returns tags[key], or def when absent/empty.
func tagOrDefault(tags map[string]string, key, def string) string {
	if v := tags[key]; v != "" {
		return v
	}

	return def
}

// conflictIfExists writes a 409 alreadyExists (or the underlying error) and
// returns true when a name-existence probe found the resource or errored; it
// returns false, letting the caller proceed, only when findErr is NotFound.
func conflictIfExists(w http.ResponseWriter, findErr error, msg string) bool {
	switch {
	case findErr == nil:
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists", msg)
		return true
	case !cerrors.IsNotFound(findErr):
		gcprest.WriteCErr(w, findErr)
		return true
	default:
		return false
	}
}

func hostOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

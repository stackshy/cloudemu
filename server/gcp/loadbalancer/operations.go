package loadbalancer

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
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

	if _, err := h.findTGByName(r.Context(), rp, req.Name); conflictIfExists(w, err, "backend service "+req.Name+" already exists") {
		return
	}

	if err := h.validateHealthCheckRefs(r.Context(), rp, req.HealthChecks); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.validateBackendRefs(r.Context(), req.Backends); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	tags := backendServiceTags(&req)
	tags[bsCreationTag] = time.Now().UTC().Format(time.RFC3339)
	tags[bsNameTag] = req.Name
	tags[bsScopeTag] = scopeKeyOf(rp)

	if _, err := h.lb.CreateTargetGroup(r.Context(), lbdriver.TargetGroupConfig{
		// A scope-prefixed driver name keeps a global and a regional backend
		// service of the same name in distinct store slots (the driver keys by
		// name); the client-facing name is preserved in bsNameTag.
		Name:     scopedDriverName(rp, req.Name),
		Protocol: req.Protocol,
		Port:     req.Port,
		// The driver TargetGroup can't hold these GCP fields, so round-trip them
		// through tags rather than dropping them on read.
		Tags: tags,
	}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
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

	if err := h.validateHealthCheckRefs(r.Context(), rp, req.HealthChecks); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.validateBackendRefs(r.Context(), req.Backends); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	err := patcher.PatchGCPBackendService(r.Context(), scopedDriverName(rp, rp.ResourceName), func(tg *lbdriver.TargetGroupInfo) {
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

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceBackendServices, rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getBackendService(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	tg, err := h.findTGByName(r.Context(), rp, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toBackendServiceResponse(tg, rp, hostOf(r)))
}

// getBackendServiceHealth serves compute.backendServices.getHealth: POST
// .../backendServices/{bs}/getHealth with a ResourceGroupReference body. The
// backend service must exist (404 otherwise). It reports HEALTHY for each
// instance actually registered in the referenced instance group (via
// addInstances), so a real user sees the members they added; when the group has
// no resolvable members it falls back to reporting the group itself as HEALTHY,
// preserving a valid BackendServiceGroupHealth shape.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getBackendServiceHealth(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if _, err := h.findTGByName(r.Context(), rp, rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var req resourceGroupReference
	if r.ContentLength != 0 && !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	resp := backendServiceGroupHealth{Kind: "compute#backendServiceGroupHealth"}
	if req.Group != "" {
		resp.HealthStatus = h.groupHealthStatus(r.Context(), req.Group)
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

// groupHealthStatus resolves an instance group reference to a HEALTHY entry per
// registered member; when the group is unknown or empty it reports the group
// reference itself as HEALTHY so the response is never empty for a named group.
func (h *Handler) groupHealthStatus(ctx context.Context, group string) []healthStatus {
	members := h.instanceGroupMembers(ctx, group)
	if len(members) == 0 {
		return []healthStatus{{HealthState: "HEALTHY", Instance: group}}
	}

	out := make([]healthStatus, 0, len(members))
	for _, m := range members {
		out = append(out, healthStatus{HealthState: "HEALTHY", Instance: m})
	}

	return out
}

// instanceGroupMembers resolves an instance-group reference (a zonal or regional
// self-link / relative path) to its registered instance URLs, or nil when the
// group is not an instance group this handler stores.
func (h *Handler) instanceGroupMembers(ctx context.Context, group string) []string {
	store, ok := h.gcpStore()
	if !ok {
		return nil
	}

	collection, scope, name := parseGroupRef(group)
	if collection == "" {
		return nil
	}

	res, err := store.GetGCPResource(ctx, collection, scope, name)
	if err != nil {
		return nil
	}

	return sortedMembers(membersOf(res.Body))
}

// parseGroupRef splits an instance-group reference into (collection, scope,
// name). A ".../zones/{z}/instanceGroups/{n}" reference maps to the zonal
// instanceGroups collection scoped by zone; ".../regions/{r}/instanceGroups/{n}"
// to regionInstanceGroups scoped by region. Any other shape returns "".
func parseGroupRef(ref string) (collection, scope, name string) {
	parts := strings.Split(ref, "/")

	for i := 0; i+1 < len(parts); i++ {
		switch parts[i] {
		case gcprest.ScopeZones:
			return resourceInstanceGroups, parts[i+1], lastPathSegment(ref)
		case gcprest.ScopeRegions:
			return resourceRegionInstanceGroups, parts[i+1], lastPathSegment(ref)
		}
	}

	return "", "", ""
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listBackendServices(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	tgs, err := h.lb.DescribeTargetGroups(r.Context(), nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostOf(r)
	scopeKey := scopeKeyOf(rp)
	filter := r.URL.Query().Get("filter")

	items := make([]backendServiceResponse, 0, len(tgs))

	for i := range tgs {
		// A global list must not leak regional backend services (and vice versa),
		// so select only this scope's records before filtering by name.
		if tgs[i].Tags[bsScopeTag] != scopeKey {
			continue
		}

		if resp := toBackendServiceResponse(&tgs[i], rp, host); gcprest.NameMatches(filter, resp.Name) {
			items = append(items, resp)
		}
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	page, err := pagination.Paginate(items, r.URL.Query().Get("pageToken"),
		gcprest.MaxResults(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := backendServiceListResponse{
		Kind:          "compute#backendServiceList",
		ID:            "projects/" + rp.Project + "/" + listScopeSegment(rp) + "/backendServices",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
		SelfLink:      gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceBackendServices, ""),
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD delete shape is duplicate-by-design across resource types
func (h *Handler) deleteBackendService(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	tg, err := h.findTGByName(r.Context(), rp, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Real GCP refuses to delete a backend service still referenced by a
	// forwarding rule or a url-map (400 resourceInUseByAnotherResource); deleting
	// it here would orphan the dependent (a dangling listener, or a url-map whose
	// service no longer resolves).
	if ref := h.backendServiceInUse(r.Context(), rp, rp.ResourceName, tg.ARN); ref != "" {
		gcprest.WriteError(w, http.StatusBadRequest, reasonResourceInUse,
			"The backend_service resource '"+rp.ResourceName+"' is already being used by '"+ref+"'")

		return
	}

	if err := h.lb.DeleteTargetGroup(r.Context(), tg.ARN); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
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

	if _, err := h.findLBByName(r.Context(), rp, req.Name); conflictIfExists(w, err, "forwarding rule "+req.Name+" already exists") {
		return
	}

	if err := h.validateForwardingRuleTarget(r.Context(), rp, req.Target); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	tags := forwardingRuleTags(&req)
	tags[frNameTag] = req.Name
	tags[frScopeTag] = scopeKeyOf(rp)

	lb, err := h.lb.CreateLoadBalancer(r.Context(), lbdriver.LBConfig{
		// A scope-prefixed driver name keeps a global and a regional forwarding
		// rule of the same name distinct; the client-facing name lives in frNameTag.
		Name:   scopedDriverName(rp, req.Name),
		Type:   "network",
		Scheme: schemeFromRule(&req),
		// Round-trip the GCP forwarding-rule fields the driver LB can't model
		// (exact scheme, portRange, IPProtocol, IPAddress, target, …) through tags.
		Tags: tags,
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
		tg, ferr := h.findTGByName(r.Context(), rp, bsName)
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

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceForwardingRules, req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	lb, err := h.findLBByName(r.Context(), rp, rp.ResourceName)
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
	scopeKey := scopeKeyOf(rp)
	filter := r.URL.Query().Get("filter")

	items := make([]forwardingRuleResponse, 0, len(lbs))

	for i := range lbs {
		if lbs[i].Tags[frScopeTag] != scopeKey {
			continue
		}

		if resp := h.toForwardingRuleResponse(r.Context(), &lbs[i], rp, host); gcprest.NameMatches(filter, resp.Name) {
			items = append(items, resp)
		}
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	page, err := pagination.Paginate(items, r.URL.Query().Get("pageToken"),
		gcprest.MaxResults(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := forwardingRuleListResponse{
		Kind:          "compute#forwardingRuleList",
		ID:            "projects/" + rp.Project + "/" + listScopeSegment(rp) + "/forwardingRules",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
		SelfLink:      gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceForwardingRules, ""),
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD delete shape is duplicate-by-design across resource types
func (h *Handler) deleteForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	lb, err := h.findLBByName(r.Context(), rp, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.lb.DeleteLoadBalancer(r.Context(), lb.ARN); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceForwardingRules, rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// --- lookups + response shaping ---

// scopeSep separates the scope key from the client name in a scope-prefixed
// driver key. A NUL byte can't appear in a GCP resource name, so it can't
// collide with a legitimate name segment.
const scopeSep = "\x00"

// scopedDriverName maps a client-facing name to the scope-qualified name used
// as the driver's store key. Global resources keep their plain name (so
// existing global links stay stable); regional ones are prefixed with the
// region so a same-name global/regional pair does not collide.
//
//nolint:gocritic // rp is a request-scoped value
func scopedDriverName(rp gcprest.ResourcePath, name string) string {
	if rp.Scope == gcprest.ScopeGlobal {
		return name
	}

	return scopeKeyOf(rp) + scopeSep + name
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) findTGByName(ctx context.Context, rp gcprest.ResourcePath, name string) (*lbdriver.TargetGroupInfo, error) {
	tgs, err := h.lb.DescribeTargetGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	key := scopedDriverName(rp, name)
	for i := range tgs {
		if tgs[i].Name == key {
			return &tgs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "backend service %q not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) findLBByName(ctx context.Context, rp gcprest.ResourcePath, name string) (*lbdriver.LBInfo, error) {
	lbs, err := h.lb.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return nil, err
	}

	key := scopedDriverName(rp, name)
	for i := range lbs {
		if lbs[i].Name == key {
			return &lbs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "forwarding rule %q not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toBackendServiceResponse(tg *lbdriver.TargetGroupInfo, rp gcprest.ResourcePath, host string) backendServiceResponse {
	name := displayName(tg.Tags, bsNameTag, tg.Name)
	resp := backendServiceResponse{
		Kind:     "compute#backendService",
		ID:       numericID(tg.ID),
		Name:     name,
		Protocol: tg.Protocol,
		Port:     tg.Port,
		SelfLink: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceBackendServices, name),
	}

	resp.Description = tg.Tags[bsDescriptionTag]
	resp.PortName = tg.Tags[bsPortNameTag]
	resp.LoadBalancingScheme = tg.Tags[bsSchemeTag]
	resp.SessionAffinity = tg.Tags[bsSessionAffinityTag]
	resp.CreationTimestamp = tg.Tags[bsCreationTag]
	// A non-empty fingerprint is required for every future patch; real GCP always
	// returns one, so derive a stable value from the resource name.
	resp.Fingerprint = fingerprintOf(name)

	if ts := tg.Tags[bsTimeoutSecTag]; ts != "" {
		if n, err := strconv.Atoi(ts); err == nil {
			resp.TimeoutSec = n
		}
	}

	if hc := tg.Tags[bsHealthChecksTag]; hc != "" {
		resp.HealthChecks = strings.Split(hc, ",")
	}

	decodeJSONTag(tg.Tags, bsBackendsTag, &resp.Backends)
	decodeJSONTag(tg.Tags, bsConnDrainTag, &resp.ConnectionDraining)
	decodeJSONTag(tg.Tags, bsCdnPolicyTag, &resp.CdnPolicy)
	resp.EnableCDN = boolTag(tg.Tags, bsEnableCDNTag)

	return resp
}

// displayName returns the client-facing name stored in nameTag, falling back to
// the driver's own name when the tag is absent (legacy records).
func displayName(tags map[string]string, nameTag, fallback string) string {
	if n := tags[nameTag]; n != "" {
		return n
	}

	return fallback
}

// decodeJSONTag unmarshals a JSON-encoded reserved tag into out, leaving out
// untouched when the tag is absent.
func decodeJSONTag(tags map[string]string, key string, out any) {
	if s := tags[key]; s != "" {
		_ = json.Unmarshal([]byte(s), out)
	}
}

// encodeJSONTag stores v as a JSON string under key.
func encodeJSONTag(tags map[string]string, key string, v any) {
	if b, err := json.Marshal(v); err == nil {
		tags[key] = string(b)
	}
}

// boolTag reads a "true"/"false" reserved tag, returning nil when absent so an
// unset enableCDN stays omitted rather than defaulting to false.
func boolTag(tags map[string]string, key string) *bool {
	s, ok := tags[key]
	if !ok {
		return nil
	}

	v := s == "true"

	return &v
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
	bsBackendsTag        = "cloudemu:gcpBsBackends"
	bsConnDrainTag       = "cloudemu:gcpBsConnectionDraining"
	bsCdnPolicyTag       = "cloudemu:gcpBsCdnPolicy"
	bsEnableCDNTag       = "cloudemu:gcpBsEnableCDN"
	// bsNameTag/bsScopeTag carry the client-facing name and scope key so a
	// scope-prefixed driver record re-emits its real name at its real scope.
	bsNameTag  = "cloudemu:gcpBsName"
	bsScopeTag = "cloudemu:gcpBsScope"
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

	mergeBackendServiceCDNTags(tags, req)
}

// mergeBackendServiceCDNTags folds the nested backend-service fields
// (backends[], connectionDraining, cdnPolicy, enableCDN) into tags as JSON so
// they round-trip through the driver's flat tag map.
func mergeBackendServiceCDNTags(tags map[string]string, req *backendServiceRequest) {
	if len(req.Backends) > 0 {
		encodeJSONTag(tags, bsBackendsTag, req.Backends)
	}

	if req.ConnectionDraining != nil {
		encodeJSONTag(tags, bsConnDrainTag, req.ConnectionDraining)
	}

	if req.CdnPolicy != nil {
		encodeJSONTag(tags, bsCdnPolicyTag, req.CdnPolicy)
	}

	if req.EnableCDN != nil {
		tags[bsEnableCDNTag] = strconv.FormatBool(*req.EnableCDN)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) toForwardingRuleResponse(ctx context.Context, lb *lbdriver.LBInfo,
	rp gcprest.ResourcePath, host string,
) forwardingRuleResponse {
	name := displayName(lb.Tags, frNameTag, lb.Name)
	out := forwardingRuleResponse{
		Kind:                "compute#forwardingRule",
		ID:                  numericID(lb.ID),
		Name:                name,
		IPAddress:           forwardingRuleIP(lb),
		IPProtocol:          tagOrDefault(lb.Tags, frProtocolTag, "TCP"),
		PortRange:           lb.Tags[frPortRangeTag],
		Target:              lb.Tags[frTargetTag],
		Description:         lb.Tags[frDescriptionTag],
		LoadBalancingScheme: forwardingRuleScheme(lb),
		CreationTimestamp:   lb.Tags[frCreationTag],
		SelfLink:            gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, resourceForwardingRules, name),
	}

	// A linked listener (a rule referencing a backend service) supersedes the
	// round-tripped protocol/portRange and adds the backendService self-link.
	if listeners, err := h.lb.DescribeListeners(ctx, lb.ARN); err == nil && len(listeners) > 0 {
		out.IPProtocol = protocolOrDefault(listeners[0].Protocol)
		out.PortRange = strconv.Itoa(listeners[0].Port)

		if tgName := h.tgNameByARN(ctx, listeners[0].TargetGroupARN); tgName != "" {
			out.BackendService = gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName,
				resourceBackendServices, tgName)
		}
	}

	return out
}

// tgNameByARN resolves a target-group ARN back to its client-facing name for
// response links.
func (h *Handler) tgNameByARN(ctx context.Context, arn string) string {
	if arn == "" {
		return ""
	}

	tgs, err := h.lb.DescribeTargetGroups(ctx, []string{arn})
	if err != nil || len(tgs) == 0 {
		return ""
	}

	return displayName(tgs[0].Tags, bsNameTag, tgs[0].Name)
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

// backendServiceInUse returns the name of a resource still referencing the
// backend service, or "" when it is safe to delete. A forwarding rule (via its
// linked listener) or a url-map (defaultService / pathMatchers) both pin it.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) backendServiceInUse(ctx context.Context, rp gcprest.ResourcePath, bsName, tgARN string) string {
	if fr := h.forwardingRuleRefTG(ctx, tgARN); fr != "" {
		return fr
	}

	return h.urlMapRefBackendService(ctx, rp, bsName)
}

// forwardingRuleRefTG returns the name of a forwarding rule whose listener
// targets tgARN, or "" when none does.
func (h *Handler) forwardingRuleRefTG(ctx context.Context, tgARN string) string {
	lbs, err := h.lb.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return ""
	}

	for i := range lbs {
		listeners, lerr := h.lb.DescribeListeners(ctx, lbs[i].ARN)
		if lerr != nil {
			continue
		}

		for j := range listeners {
			if listeners[j].TargetGroupARN == tgARN {
				return displayName(lbs[i].Tags, frNameTag, lbs[i].Name)
			}
		}
	}

	return ""
}

// urlMapRefBackendService returns the name of a same-scope url-map referencing
// bsName (as defaultService or in a pathMatcher), or "" when none does.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) urlMapRefBackendService(ctx context.Context, rp gcprest.ResourcePath, bsName string) string {
	store, ok := h.gcpStore()
	if !ok {
		return ""
	}

	maps, err := store.ListGCPResources(ctx, resourceURLMaps, scopeKeyOf(rp))
	if err != nil {
		return ""
	}

	for i := range maps {
		if bodyRefsBackendService(maps[i].Body, bsName) {
			return maps[i].Name
		}
	}

	return ""
}

// bodyRefsBackendService walks an opaque url-map body for any "service" /
// "defaultService" member whose reference resolves to bsName.
func bodyRefsBackendService(v any, bsName string) bool {
	switch t := v.(type) {
	case map[string]any:
		return mapRefsBackendService(t, bsName)
	case []any:
		for i := range t {
			if bodyRefsBackendService(t[i], bsName) {
				return true
			}
		}
	}

	return false
}

// mapRefsBackendService checks one map node for a backend-service reference and
// recurses into its members.
func mapRefsBackendService(m map[string]any, bsName string) bool {
	for k, val := range m {
		if k == "service" || k == "defaultService" {
			if s, ok := val.(string); ok && backendServiceName(s) == bsName {
				return true
			}
		}

		if bodyRefsBackendService(val, bsName) {
			return true
		}
	}

	return false
}

// validateHealthCheckRefs rejects a create/patch whose healthChecks[] names a
// health check that does not exist in the same scope, matching real GCP's
// "Invalid value for field 'resource.healthChecks[N]'" rejection. It is a no-op
// when the driver has no GCP resource store (the health checks can't be
// resolved) so non-GCP drivers stay unaffected.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateHealthCheckRefs(ctx context.Context, rp gcprest.ResourcePath, refs []string) error {
	if len(refs) == 0 {
		return nil
	}

	store, ok := h.gcpStore()
	if !ok {
		return nil
	}

	scope := scopeKeyOf(rp)

	for i, ref := range refs {
		name := lastPathSegment(ref)

		_, err := store.GetGCPResource(ctx, resourceHealthChecks, scope, name)
		if err == nil {
			continue
		}

		if cerrors.IsNotFound(err) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value for field 'resource.healthChecks[%d]': '%s'. The referenced health check resource cannot be found.", i, ref)
		}

		return err
	}

	return nil
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
	frTargetTag      = "cloudemu:gcpFrTarget"
	// frNameTag/frScopeTag carry the client-facing name and scope key so a
	// scope-prefixed driver record re-emits its real name at its real scope.
	frNameTag  = "cloudemu:gcpFrName"
	frScopeTag = "cloudemu:gcpFrScope"
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

	if req.Target != "" {
		tags[frTargetTag] = req.Target
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

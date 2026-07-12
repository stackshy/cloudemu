package loadbalancer

import (
	"context"
	"net/http"
	"strconv"
	"strings"

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

	if _, err := h.lb.CreateTargetGroup(r.Context(), lbdriver.TargetGroupConfig{
		Name:     req.Name,
		Protocol: req.Protocol,
		Port:     req.Port,
	}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, gcprest.ScopeGlobal, "",
		resourceBackendServices, req.Name, "insert")

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

//nolint:gocritic // rp is a request-scoped value
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

	lb, err := h.lb.CreateLoadBalancer(r.Context(), lbdriver.LBConfig{
		Name:   req.Name,
		Type:   "network",
		Scheme: schemeFromRule(&req),
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

//nolint:gocritic // rp is a request-scoped value
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
	return backendServiceResponse{
		Kind:     "compute#backendService",
		ID:       numericID(tg.ID),
		Name:     tg.Name,
		Protocol: tg.Protocol,
		Port:     tg.Port,
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceBackendServices, tg.Name),
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
		IPAddress:           lb.DNSName,
		IPProtocol:          "TCP",
		LoadBalancingScheme: schemeToGCP(lb.Scheme),
		SelfLink:            gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceForwardingRules, lb.Name),
	}

	// Reflect the backing backend service, if a listener links one.
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

// schemeFromRule maps a GCP loadBalancingScheme to the driver scheme.
func schemeFromRule(req *forwardingRuleRequest) string {
	if strings.EqualFold(req.LoadBalancingScheme, "INTERNAL") {
		return "internal"
	}

	return "internet-facing"
}

// schemeToGCP maps the driver scheme back to a GCP loadBalancingScheme.
func schemeToGCP(scheme string) string {
	if scheme == "internal" {
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

// numericID returns a stable uint64-shaped string derived from a driver ID.
// GCP wire IDs are uint64 and proto JSON unmarshalling rejects anything else.
func numericID(driverID string) string {
	const fnvOffset uint64 = 14695981039346656037

	const fnvPrime uint64 = 1099511628211

	h := fnvOffset
	for i := 0; i < len(driverID); i++ {
		h ^= uint64(driverID[i])
		h *= fnvPrime
	}

	return strconv.FormatUint(h, 10)
}

func hostOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

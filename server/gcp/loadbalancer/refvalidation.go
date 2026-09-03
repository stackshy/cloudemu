package loadbalancer

// Upstream reference validation for the L7 front-end chain. Real GCP rejects a
// create/patch/set-action whose reference names a resource that does not exist
// in the same scope (400 invalid, "Invalid value for field ..."). This file
// centralizes those checks alongside the reference-integrity delete guards in
// l7frontend.go so no create/update in the chain can point at a resource that
// isn't there:
//
//	urlMap.defaultService / pathMatchers[].defaultService / pathRules[].service → backendService
//	targetHttp(s)Proxy.urlMap                                                   → urlMap
//	targetHttpsProxy.sslCertificates[]                                          → sslCertificate
//	forwardingRule.target                                                      → targetHttp(s)Proxy / targetPool
//	backendService.backends[].group                                            → instanceGroup / regionInstanceGroup
import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// validGCPBalancingModes are the GCP-recognized backendService.backends[].balancingMode
// values; an omitted mode defaults to UTILIZATION in real GCP and is accepted here too.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var validGCPBalancingModes = map[string]bool{
	"":            true,
	"UTILIZATION": true,
	"RATE":        true,
	"CONNECTION":  true,
}

// validateGCPResourceRefs rejects an opaque-resource create/patch body whose
// upstream reference names a resource that does not exist in the same scope.
// It is a no-op for collections without a validated upstream reference (a
// health check, target pool, ssl certificate, or instance group has none).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateGCPResourceRefs(ctx context.Context, rp gcprest.ResourcePath, body map[string]any) error {
	switch rp.ResourceType {
	case resourceURLMaps:
		return h.validateURLMapServiceRefs(ctx, rp, body)
	case resourceTargetHTTPProxies, resourceTargetHTTPSProxies:
		return h.validateProxyRefs(ctx, rp, body)
	default:
		return nil
	}
}

// namedRef is one upstream reference found while walking a request body: the
// JSON field it came from (for the error message) and the reference value.
type namedRef struct {
	field string
	value string
}

// collectRefs walks v (a decoded JSON body) collecting every string value of a
// member whose key is in fields, recursing into maps and slices so a url-map's
// nested pathMatchers[].pathRules[].service is found alongside its top-level
// defaultService.
func collectRefs(v any, fields map[string]bool, out *[]namedRef) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if fields[k] {
				if s, ok := val.(string); ok && s != "" {
					*out = append(*out, namedRef{field: k, value: s})
				}
			}

			collectRefs(val, fields, out)
		}
	case []any:
		for _, e := range t {
			collectRefs(e, fields, out)
		}
	}
}

// validateURLMapServiceRefs rejects a url-map body whose defaultService, or any
// nested pathMatchers[].defaultService / pathRules[].service, names a backend
// service that does not exist in the same scope.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateURLMapServiceRefs(ctx context.Context, rp gcprest.ResourcePath, body map[string]any) error {
	var refs []namedRef

	collectRefs(body, map[string]bool{"service": true, "defaultService": true}, &refs)

	for _, ref := range refs {
		if _, err := h.findTGByName(ctx, rp, backendServiceName(ref.value)); err != nil {
			if cerrors.IsNotFound(err) {
				return invalidRefErr(ref.field, ref.value, "backend service")
			}

			return err
		}
	}

	return nil
}

// validateProxyRefs rejects a target-proxy body whose urlMap, or (https proxies
// only) sslCertificates[], names a resource that does not exist in the same
// scope.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateProxyRefs(ctx context.Context, rp gcprest.ResourcePath, body map[string]any) error {
	if ref, ok := body[fieldURLMap].(string); ok && ref != "" {
		if err := h.requireGCPResource(ctx, rp, resourceURLMaps, ref, fieldURLMap); err != nil {
			return err
		}
	}

	if rp.ResourceType != resourceTargetHTTPSProxies {
		return nil
	}

	certs, _ := body[fieldSslCertificates].([]any)

	for _, c := range certs {
		ref, ok := c.(string)
		if !ok || ref == "" {
			continue
		}

		if err := h.requireGCPResource(ctx, rp, resourceSslCertificates, ref, fieldSslCertificates); err != nil {
			return err
		}
	}

	return nil
}

// validateProxySetRequest rejects a setUrlMap/setSslCertificates action body
// whose reference names a resource that does not exist in the same scope.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateProxySetRequest(ctx context.Context, rp gcprest.ResourcePath, req *setProxyRequest, list bool) error {
	if !list {
		if req.URLMap == "" {
			return nil
		}

		return h.requireGCPResource(ctx, rp, resourceURLMaps, req.URLMap, fieldURLMap)
	}

	for _, cert := range req.SslCertificates {
		if cert == "" {
			continue
		}

		if err := h.requireGCPResource(ctx, rp, resourceSslCertificates, cert, fieldSslCertificates); err != nil {
			return err
		}
	}

	return nil
}

// requireGCPResource rejects when ref (a self-link or bare name) does not
// resolve to an existing collection resource in rp's scope. A driver with no
// GCP resource store leaves the reference unvalidated rather than blocking
// every create.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) requireGCPResource(ctx context.Context, rp gcprest.ResourcePath, collection, ref, field string) error {
	store, ok := h.gcpStore()
	if !ok {
		return nil
	}

	name := lastPathSegment(ref)

	_, err := store.GetGCPResource(ctx, collection, scopeKeyOf(rp), name)
	if err == nil {
		return nil
	}

	if cerrors.IsNotFound(err) {
		return invalidRefErr(field, ref, singularOf(collection))
	}

	return err
}

// invalidRefErr renders the GCP "Invalid value for field" InvalidArgument
// rejection for a dangling upstream reference.
func invalidRefErr(field, ref, noun string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"Invalid value for field 'resource.%s': '%s'. The referenced %s resource cannot be found.", field, ref, noun)
}

// validateForwardingRuleTarget rejects a forwarding rule whose target names a
// target-proxy or target-pool resource that does not exist in the same scope.
// Only the collections this handler implements are recognized; a reference to
// an unimplemented target type (targetSslProxies, targetTcpProxies, …) is left
// unvalidated rather than falsely rejected.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateForwardingRuleTarget(ctx context.Context, rp gcprest.ResourcePath, target string) error {
	if target == "" {
		return nil
	}

	collection := targetCollectionFor(target)
	if collection == "" {
		return nil
	}

	return h.requireGCPResource(ctx, rp, collection, target, "target")
}

// targetCollectionFor extracts the collection segment of a forwarding rule's
// target reference, recognizing only the collections this handler implements.
func targetCollectionFor(ref string) string {
	for _, c := range []string{resourceTargetHTTPProxies, resourceTargetHTTPSProxies, resourceTargetPools} {
		if strings.Contains(ref, "/"+c+"/") {
			return c
		}
	}

	return ""
}

// validateBackendRefs rejects a backend-service create/patch whose backends[]
// names an instance group that does not exist, or sets an unrecognized
// balancingMode. NEG and other non-instance-group backend shapes are not
// modeled by the opaque instance-group store, so only instance-group
// references (zonal/regional) are checked.
func (h *Handler) validateBackendRefs(ctx context.Context, backends []backend) error {
	store, ok := h.gcpStore()

	for i := range backends {
		if !validGCPBalancingModes[backends[i].BalancingMode] {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value for field 'resource.backends[%d].balancingMode': '%s'.", i, backends[i].BalancingMode)
		}

		if !ok || backends[i].Group == "" {
			continue
		}

		collection, scope, name := parseGroupRef(backends[i].Group)
		if collection == "" {
			continue
		}

		if _, err := store.GetGCPResource(ctx, collection, scope, name); err != nil {
			if cerrors.IsNotFound(err) {
				return cerrors.Newf(cerrors.InvalidArgument,
					"Invalid value for field 'resource.backends[%d].group': '%s'. The referenced instance group resource cannot be found.",
					i, backends[i].Group)
			}

			return err
		}
	}

	return nil
}

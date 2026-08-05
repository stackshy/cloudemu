package kubernetes

import (
	"net/http"
	"strings"
)

// serveDiscovery answers the Kubernetes API discovery endpoints.
//
// Without these the emulator is usable only by code that hits a resource path
// directly (e.g. client-go's typed clients). Every tool that NEGOTIATES first —
// kubectl and helm both do — fails at startup with:
//
//	couldn't get current server API group list: the server could not find the
//	requested resource
//
// so it never reaches the resources that are implemented. Discovery is
// therefore not a nicety: it is what makes the working data plane reachable by
// real tooling.
//
// The advertised set is deliberately derived from what ClusterState.ServeHTTP
// actually dispatches, so discovery can never promise a resource the emulator
// does not serve. A tool that trusts discovery and then 404s is worse than one
// that cannot start.
//
// Returns false when the path is not a discovery request, so the caller falls
// through to normal resource routing.
func (s *ClusterState) serveDiscovery(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/api":
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":       "APIVersions",
			"apiVersion": "v1",
			"versions":   []string{"v1"},
			"serverAddressByClientCIDRs": []map[string]string{
				{"clientCIDR": "0.0.0.0/0", "serverAddress": r.Host},
			},
		})

		return true

	case "/apis":
		groups := make([]map[string]any, 0)
		for _, gv := range s.discoveryGroups() {
			groups = append(groups, apiGroup(gv.group, gv.version))
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"kind":       "APIGroupList",
			"apiVersion": "v1",
			"groups":     groups,
		})

		return true

	case "/api/v1":
		writeJSON(w, http.StatusOK, apiResourceList("", "v1", s.coreResources()))

		return true

	// OpenAPI (v2 JSON/protobuf and v3) is served cluster-independently by
	// serveOpenAPI, intercepted in APIServer.ServeHTTP before this handler —
	// helm and kubectl both validate rendered manifests against it.

	// /version is not discovery proper, but kubectl and helm both probe it and
	// some code paths refuse to proceed without a parseable server version.
	case "/version":
		writeJSON(w, http.StatusOK, map[string]any{
			"major": "1", "minor": "29",
			"gitVersion": "v1.29.0-cloudemu",
			"platform":   "cloudemu/amd64",
		})

		return true
	}

	// Group-version discovery: /apis/<group>/<version>. Derived from the
	// registry (plus the typed apps/policy groups) so every served group and
	// its resources — including subresources — are advertised.
	if res, gv, group, ok := s.groupVersionDiscovery(r.URL.Path); ok {
		writeJSON(w, http.StatusOK, apiResourceList(group, gv, res))

		return true
	}

	return false
}

// groupVersion pairs an API group with the version this server serves it at.
type groupVersion struct {
	group   string
	version string
}

// discoveryGroups (method) advertises groups from the LIVE registry, so a CRD
// created at runtime surfaces its group in discovery immediately.
func (s *ClusterState) discoveryGroups() []groupVersion {
	return discoveryGroupsFrom(s.reg.allDefs())
}

// discoveryGroupsFrom lists the non-core API groups the server serves, each with
// a representative version, built from the typed handlers (apps, policy), the
// aggregated metrics.k8s.io and authorization.k8s.io APIs, plus the supplied
// defs so new groups surface automatically.
func discoveryGroupsFrom(defs []*resourceDef) []groupVersion {
	seen := map[string]bool{"apps": true, "policy": true, apiGroupMetrics: true, apiGroupAuthorization: true}
	out := []groupVersion{
		{"apps", "v1"}, {"policy", "v1"},
		{apiGroupMetrics, apiVersionMetrics}, {apiGroupAuthorization, apiVersionV1},
	}

	for _, d := range defs {
		if d.group == "" || seen[d.group] {
			continue
		}

		seen[d.group] = true

		out = append(out, groupVersion{d.group, d.version})
	}

	return out
}

// groupVersionDiscovery returns the resource list for a /apis/<group>/<version>
// path, or ok=false if the path isn't a served group-version. Reads the live
// registry so CRD group-versions resolve.
func (s *ClusterState) groupVersionDiscovery(path string) (res []apiResource, groupVersionStr, group string, ok bool) {
	parts := splitPath(strings.TrimSuffix(path, "/"))
	if len(parts) != 3 || parts[0] != pathSegAPIs {
		return nil, "", "", false
	}

	group, version := parts[1], parts[2]

	switch {
	case group == apiGroupApps && version == apiVersionV1:
		return s.appsResources(), "apps/v1", apiGroupApps, true
	case group == apiGroupPolicy && version == apiVersionV1:
		return policyResources(), "policy/v1", apiGroupPolicy, true
	case group == apiGroupAuthorization && version == apiVersionV1:
		return authorizationResources(), apiGroupAuthorization + "/v1", apiGroupAuthorization, true
	default:
		r := registryAPIResourcesFrom(s.reg.allDefs(), group, version)
		if len(r) == 0 {
			return nil, "", "", false
		}

		return r, group + "/" + version, group, true
	}
}

func apiGroup(name, version string) map[string]any {
	gv := name + "/" + version

	return map[string]any{
		"name": name,
		"versions": []map[string]string{
			{"groupVersion": gv, "version": version},
		},
		"preferredVersion": map[string]string{"groupVersion": gv, "version": version},
	}
}

func apiResourceList(group, groupVersion string, res []apiResource) map[string]any {
	out := make([]map[string]any, 0, len(res))

	for _, x := range res {
		entry := map[string]any{
			"name":         x.Name,
			"singularName": x.Singular,
			"namespaced":   x.Namespaced,
			"kind":         x.Kind,
			"group":        group,
			"verbs":        x.Verbs,
		}
		// shortNames is what makes `kubectl get ns` / `get deploy` resolve;
		// without it kubectl reports "the server doesn't have a resource type",
		// which reads like the resource is missing rather than un-aliased.
		if len(x.Short) > 0 {
			entry["shortNames"] = x.Short
		}

		out = append(out, entry)
	}

	return map[string]any{
		"kind":         "APIResourceList",
		"apiVersion":   "v1",
		"groupVersion": groupVersion,
		"resources":    out,
	}
}

type apiResource struct {
	Name       string
	Singular   string
	Kind       string
	Namespaced bool
	Verbs      []string
	Short      []string
}

// rwVerbs is what the implemented handlers actually support. Advertising
// anything beyond this would have tooling attempt calls that 405.
func rwVerbs() []string {
	return []string{"get", "list", "watch", "create", "update", "patch", "delete"}
}

// coreResources (method) reads the live registry so CRD core-group kinds (rare,
// but allowed) surface. coreResourcesFrom is the pure form OpenAPI uses.
func (s *ClusterState) coreResources() []apiResource {
	return coreResourcesFrom(s.reg.allDefs())
}

func coreResourcesFrom(defs []*resourceDef) []apiResource {
	reg := registryAPIResourcesFrom(defs, "", "v1")

	const typedCoreKinds = 7

	res := make([]apiResource, 0, typedCoreKinds+len(reg))
	res = append(res,
		apiResource{"namespaces", "namespace", "Namespace", false, rwVerbs(), []string{"ns"}},
		apiResource{"configmaps", "configmap", "ConfigMap", true, rwVerbs(), []string{"cm"}},
		apiResource{"pods", "pod", "Pod", true, rwVerbs(), []string{"po"}},
		apiResource{"secrets", "secret", "Secret", true, rwVerbs(), nil},
		apiResource{"serviceaccounts", "serviceaccount", "ServiceAccount", true, rwVerbs(), []string{"sa"}},
		apiResource{"services", "service", "Service", true, rwVerbs(), []string{"svc"}},
		// Endpoints are managed by the emulator (auto-created per Service and torn
		// down with it), so only the read verbs serveEndpoints implements are
		// advertised — promising create/update/delete would have kubectl and
		// client-go issue writes that 405.
		apiResource{"endpoints", "endpoints", "Endpoints", true, []string{"get", "list", "watch"}, []string{"ep"}},
	)

	return append(res, reg...)
}

// registryAPIResources expands the registry-backed kinds of a group/version
// into discovery entries (the resource plus its /status and /scale
// subresources), so discovery is derived from the registry and can't drift
// from what the server actually serves.
func registryAPIResourcesFrom(defs []*resourceDef, group, version string) []apiResource {
	out := make([]apiResource, 0, len(defs))

	for _, d := range defs {
		if d.group != group || d.version != version {
			continue
		}

		out = append(out, apiResource{d.plural, strings.ToLower(d.kind), d.kind, d.namespaced, rwVerbs(), registryShortNames[d.plural]})

		if d.hasStatus {
			out = append(out, apiResource{
				d.plural + "/status", "", d.kind, d.namespaced, subresourceVerbs(), nil,
			})
		}

		if d.hasScale {
			out = append(out, apiResource{
				d.plural + "/scale", "", "Scale", d.namespaced, subresourceVerbs(), nil,
			})
		}
	}

	return out
}

func subresourceVerbs() []string { return []string{"get", "patch", "update"} }

// registryShortNames maps a registry resource's plural to the kubectl short
// names real clusters advertise, so `kubectl get pvc/hpa/sts/...` resolves.
//
//nolint:gochecknoglobals // immutable package-level lookup table.
var registryShortNames = map[string][]string{
	"persistentvolumeclaims":    {"pvc"},
	"persistentvolumes":         {"pv"},
	"horizontalpodautoscalers":  {"hpa"},
	"statefulsets":              {"sts"},
	"replicasets":               {"rs"},
	"daemonsets":                {"ds"},
	"cronjobs":                  {"cj"},
	"ingresses":                 {"ing"},
	"networkpolicies":           {"netpol"},
	"storageclasses":            {"sc"},
	"resourcequotas":            {"quota"},
	"limitranges":               {"limits"},
	"events":                    {"ev"},
	"nodes":                     {"no"},
	"customresourcedefinitions": {"crd", "crds"},
}

func policyResources() []apiResource {
	// Only the verbs pdb.go implements. Advertising watch would have client-go
	// reflectors open a watch that returns a list and never streams, and
	// advertising patch would have kubectl and helm send a PATCH that 405s —
	// both failures land in the caller, far from the discovery document that
	// promised them.
	return []apiResource{
		{"poddisruptionbudgets", "poddisruptionbudget", "PodDisruptionBudget", true,
			[]string{"get", "list", "create", "update", "delete"}, []string{"pdb"}},
	}
}

// appsResources (method) reads the live registry. appsResourcesFrom is the pure
// form OpenAPI uses.
func (s *ClusterState) appsResources() []apiResource {
	return appsResourcesFrom(s.reg.allDefs())
}

func appsResourcesFrom(defs []*resourceDef) []apiResource {
	reg := registryAPIResourcesFrom(defs, apiGroupApps, "v1")

	const typedAppsEntries = 3

	res := make([]apiResource, 0, typedAppsEntries+len(reg))
	res = append(res,
		apiResource{"deployments", "deployment", "Deployment", true, rwVerbs(), []string{"deploy"}},
		apiResource{"deployments/status", "", "Deployment", true, subresourceVerbs(), nil},
		apiResource{"deployments/scale", "", "Scale", true, subresourceVerbs(), nil},
	)

	return append(res, reg...)
}

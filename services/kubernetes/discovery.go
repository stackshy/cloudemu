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
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":       "APIGroupList",
			"apiVersion": "v1",
			"groups": []map[string]any{
				apiGroup("apps", "v1"),
				apiGroup("policy", "v1"),
			},
		})

		return true

	case "/api/v1":
		writeJSON(w, http.StatusOK, apiResourceList("", "v1", coreResources()))

		return true

	case "/apis/apps/v1":
		writeJSON(w, http.StatusOK, apiResourceList("apps", "apps/v1", appsResources()))

		return true

	case "/apis/policy/v1":
		writeJSON(w, http.StatusOK, apiResourceList("policy", "policy/v1", policyResources()))

		return true

	// helm validates rendered manifests against the server's OpenAPI schema
	// unless the caller passes --disable-openapi-validation. Without this
	// endpoint `helm install` fails at "failed to download openapi: the server
	// could not find the requested resource" — after successfully rendering
	// the chart, which makes it look like a chart problem rather than a
	// missing server capability.
	//
	// The document is intentionally MINIMAL: a valid Swagger 2.0 envelope with
	// no definitions. helm's validator skips kinds it finds no schema for, so
	// an empty definitions map means "nothing to contradict" rather than
	// "everything is invalid". Publishing hand-written partial schemas would be
	// worse — a subtly wrong schema rejects valid manifests, and the emulator
	// would be asserting API shapes it does not actually enforce.
	case "/openapi/v2":
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, http.StatusOK, map[string]any{
			"swagger": "2.0",
			"info": map[string]any{
				"title":   "cloudemu-kubernetes",
				"version": "v1.29.0-cloudemu",
			},
			"paths":       map[string]any{},
			"definitions": map[string]any{},
		})

		return true

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

	return false
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

func coreResources() []apiResource {
	res := []apiResource{
		{"namespaces", "namespace", "Namespace", false, rwVerbs(), []string{"ns"}},
		{"configmaps", "configmap", "ConfigMap", true, rwVerbs(), []string{"cm"}},
		{"pods", "pod", "Pod", true, rwVerbs(), []string{"po"}},
		{"secrets", "secret", "Secret", true, rwVerbs(), nil},
		{"serviceaccounts", "serviceaccount", "ServiceAccount", true, rwVerbs(), []string{"sa"}},
		{"services", "service", "Service", true, rwVerbs(), []string{"svc"}},
	}

	return append(res, registryAPIResources("", "v1")...)
}

// registryAPIResources expands the registry-backed kinds of a group/version
// into discovery entries (the resource plus its /status and /scale
// subresources), so discovery is derived from the registry and can't drift
// from what the server actually serves.
func registryAPIResources(group, version string) []apiResource {
	var out []apiResource

	for _, d := range registeredResources() {
		if d.group != group || d.version != version {
			continue
		}

		out = append(out, apiResource{d.plural, strings.ToLower(d.kind), d.kind, d.namespaced, rwVerbs(), nil})

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

func appsResources() []apiResource {
	res := []apiResource{
		{"deployments", "deployment", "Deployment", true, rwVerbs(), []string{"deploy"}},
		{"deployments/status", "", "Deployment", true, subresourceVerbs(), nil},
		{"deployments/scale", "", "Scale", true, subresourceVerbs(), nil},
	}

	return append(res, registryAPIResources("apps", "v1")...)
}

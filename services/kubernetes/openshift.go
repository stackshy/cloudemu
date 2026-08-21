package kubernetes

import (
	"net/http"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Flavor selects which API surface a cluster serves. A Kubernetes-flavored
// cluster serves only the upstream groups (what EKS/AKS/GKE emulate); an
// OpenShift-flavored cluster additionally serves the *.openshift.io groups and
// boots with the cluster-identity singletons (ClusterVersion, Infrastructure)
// that `oc` and the OpenShift SDKs read first.
//
// OpenShift is a strict superset of Kubernetes, so an OpenShift cluster is a
// Kubernetes cluster plus the extra groups — the flavor only ever ADDS surface.
type Flavor int

const (
	// FlavorKubernetes is a vanilla upstream cluster (the default; what
	// RegisterCluster and the EKS/AKS/GKE control planes create).
	FlavorKubernetes Flavor = iota
	// FlavorOpenShift additionally serves the *.openshift.io groups and seeds
	// the OpenShift cluster-identity singletons. Created via
	// RegisterClusterWithFlavor and the ROSA/ARO control planes.
	FlavorOpenShift
)

// OpenShift API group names. Grouped here so the per-group registration files
// (openshift_route.go, openshift_apps.go, …) share one authoritative set.
const (
	apiGroupOSApps          = "apps.openshift.io"
	apiGroupOSAuthorization = "authorization.openshift.io"
	apiGroupOSBuild         = "build.openshift.io"
	apiGroupOSConfig        = "config.openshift.io"
	apiGroupOSImage         = "image.openshift.io"
	apiGroupOSOAuth         = "oauth.openshift.io"
	apiGroupOSProject       = "project.openshift.io"
	apiGroupOSQuota         = "quota.openshift.io"
	apiGroupOSRoute         = "route.openshift.io"
	apiGroupOSSecurity      = "security.openshift.io"
	apiGroupOSTemplate      = "template.openshift.io"
	apiGroupOSUser          = "user.openshift.io"
	apiGroupOSConsole       = "console.openshift.io"
	apiGroupOSOperator      = "operator.openshift.io"
	apiGroupOSMachine       = "machine.openshift.io"
	apiGroupOSAutoscaling   = "autoscaling.openshift.io"
)

// Emulated OpenShift release. OCP 4.16 ships Kubernetes 1.29 — the same version
// this emulator reports at /version — so a 4.16 identity keeps the OpenShift
// layer internally consistent with the Kubernetes layer beneath it.
const (
	openShiftVersion = "4.16.0"
	openShiftChannel = "stable-4.16"
)

// openshiftRegistryDefs returns the resourceDefs for every base OKD/OCP
// *.openshift.io kind. It is concatenated into a cluster's registry ONLY when
// the cluster is OpenShift-flavored, so Kubernetes clusters never advertise or
// serve these groups. Each group lives in its own file (openshift_<group>.go),
// mirroring registry_defs.go's per-group split.
func openshiftRegistryDefs() []*resourceDef {
	return concat(
		openshiftConfigDefs(),
		openshiftAppsDefs(),
		openshiftRouteDefs(),
		openshiftBuildDefs(),
		openshiftImageDefs(),
		openshiftProjectDefs(),
		openshiftUserDefs(),
		openshiftOAuthDefs(),
		openshiftSecurityDefs(),
		openshiftQuotaDefs(),
		openshiftAuthorizationDefs(),
		openshiftTemplateDefs(),
		openshiftConsoleDefs(),
		openshiftOperatorDefs(),
		openshiftMachineDefs(),
		openshiftAutoscalingDefs(),
	)
}

// openshiftConfigDefs registers the config.openshift.io/v1 kinds. These are the
// cluster-scoped operator/identity singletons (ClusterVersion, Infrastructure,
// and the config knobs `oc get <singleton> cluster` reads). They are plain CRUD
// stores — an emulator has no operators to reconcile them — but registering
// them makes `oc get clusterversion|infrastructure|...` and the OpenShift
// config client round-trip.
func openshiftConfigDefs() []*resourceDef {
	defs := make([]*resourceDef, 0, len(openshiftConfigSingletonKinds))
	for _, k := range openshiftConfigSingletonKinds {
		defs = append(defs, &resourceDef{
			group: apiGroupOSConfig, version: "v1", kind: k.kind, listKind: k.kind + "List",
			plural: k.plural, namespaced: false, hasStatus: true,
		})
	}

	return defs
}

// openshiftConfigSingletonKinds is the config.openshift.io/v1 cluster-scoped
// singleton surface (captured from a live OCP 4.21 cluster). All are
// cluster-scoped with a status subresource.
//
//nolint:gochecknoglobals // immutable registration table.
var openshiftConfigSingletonKinds = []struct{ kind, plural string }{
	{"APIServer", "apiservers"},
	{"Authentication", "authentications"},
	{"Build", "builds"},
	{"ClusterOperator", "clusteroperators"},
	{"ClusterVersion", "clusterversions"},
	{"Console", "consoles"},
	{"DNS", "dnses"},
	{"FeatureGate", "featuregates"},
	{"Image", "images"},
	{"Infrastructure", "infrastructures"},
	{"Ingress", "ingresses"},
	{"Network", "networks"},
	{"Node", "nodes"},
	{"OAuth", "oauths"},
	{"OperatorHub", "operatorhubs"},
	{"Project", "projects"},
	{"Proxy", "proxies"},
	{"Scheduler", "schedulers"},
}

// serveOpenShiftIntercept handles the OpenShift-only pseudo-endpoints that have
// no registry store or Route shape — `oc whoami` (GET users/~) and the POST-only
// RPCs (project requests, build instantiate, processed templates, authorization
// reviews). It returns true when it served the request. No-op (false) for
// Kubernetes-flavored clusters.
func (s *ClusterState) serveOpenShiftIntercept(w http.ResponseWriter, r *http.Request) bool {
	if s.flavor != FlavorOpenShift {
		return false
	}

	switch r.Method {
	case http.MethodGet:
		if isWhoamiPath(r.URL.Path) {
			s.serveWhoami(w, r)

			return true
		}
		// `oc new-project` GETs the projectrequests collection before POSTing
		// (its "can I request projects?" probe). projectrequests is a virtual
		// verb, not a stored kind, so answer the GET with an empty list — without
		// it the GET falls through to the registry and 404s, aborting the command.
		if r.URL.Path == projectRequestPath {
			serveProjectRequestList(w)

			return true
		}
	case http.MethodPost:
		return s.serveOpenShiftPost(w, r)
	}

	return false
}

// serveOpenShiftPost dispatches the POST-only OpenShift RPCs. Returns true when
// it served the request.
func (s *ClusterState) serveOpenShiftPost(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case projectRequestPath:
		s.serveProjectRequest(w, r)
	case selfSubjectReviewPath:
		s.serveSelfSubjectReview(w, r)
	default:
		return s.serveOpenShiftPostByShape(w, r)
	}

	return true
}

// serveOpenShiftPostByShape handles the POST RPCs whose target is encoded in the
// path shape (build instantiate, processed templates, authorization reviews).
func (s *ClusterState) serveOpenShiftPostByShape(w http.ResponseWriter, r *http.Request) bool {
	if ns, bc, ok := buildInstantiateTarget(r.URL.Path); ok {
		s.serveBuildInstantiate(w, ns, bc)

		return true
	}

	if _, ok := processedTemplateTarget(r.URL.Path); ok {
		s.serveProcessedTemplate(w, r)

		return true
	}

	if plural := openShiftReviewKind(r.URL.Path); plural != "" {
		s.serveOpenShiftReview(w, r, plural)

		return true
	}

	return false
}

// seedOpenShiftSingletonsLocked populates an OpenShift-flavored cluster's
// registry with the identity singletons a fresh cluster always has: the
// ClusterVersion ("version") and Infrastructure ("cluster") objects. Both are
// cluster-scoped config.openshift.io/v1 kinds. Callers hold no lock yet — this
// runs during newClusterState before the state is published, so it writes the
// stores directly (the pattern state.go uses to seed the synthetic Node).
func (s *ClusterState) seedOpenShiftSingletonsLocked() {
	if st := s.reg.getStore(apiGroupOSConfig, "v1", "clusterversions"); st != nil {
		cv := newClusterVersionObject()
		st.items[objKey("", cv.GetName())] = cv
	}

	if st := s.reg.getStore(apiGroupOSConfig, "v1", "infrastructures"); st != nil {
		infra := newInfrastructureObject()
		st.items[objKey("", infra.GetName())] = infra
	}
}

// newClusterVersionObject builds the singleton ClusterVersion ("version") a
// fresh OpenShift cluster reports. Shape follows a live OCP cluster: spec
// carries the channel and a synthetic clusterID; status reports the desired
// release as available and Progressing=False/Available=True.
func newClusterVersionObject() *unstructured.Unstructured {
	cv := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupOSConfig + "/v1",
		"kind":       "ClusterVersion",
		"metadata": map[string]any{
			"name":              "version",
			"creationTimestamp": nil,
		},
		"spec": map[string]any{
			"channel":   openShiftChannel,
			"clusterID": newClusterID(),
		},
		"status": map[string]any{
			"desired": map[string]any{
				"version": openShiftVersion,
				"image":   "quay.io/openshift-release-dev/ocp-release@sha256:" + newUID() + newUID(),
			},
			"observedGeneration": int64(1),
			"versionHash":        newUID()[:22],
			"conditions": []any{
				map[string]any{"type": "Available", "status": "True", "message": "Done applying " + openShiftVersion},
				map[string]any{"type": "Failing", "status": "False"},
				map[string]any{"type": "Progressing", "status": "False", "message": "Cluster version is " + openShiftVersion},
			},
			"history": []any{
				map[string]any{"state": "Completed", "version": openShiftVersion, "verified": false},
			},
		},
	}}
	cv.SetUID(types.UID(newUID()))
	cv.SetResourceVersion("1")
	cv.SetGeneration(1)

	return cv
}

// newClusterID formats a fresh 32-hex UID as the 8-4-4-4-12 UUID a
// ClusterVersion.spec.clusterID carries.
func newClusterID() string {
	u := newUID()

	return u[0:8] + "-" + u[8:12] + "-" + u[12:16] + "-" + u[16:20] + "-" + u[20:32]
}

// newInfrastructureObject builds the singleton Infrastructure ("cluster") a
// fresh OpenShift cluster reports. Shape follows a live cluster's status:
// infrastructureName, apiServerURL, and platform. Platform is reported as None
// (bare metal / generic) here; the ROSA/ARO control planes overwrite it with
// AWS/Azure specifics when they provision a cloud-hosted cluster.
func newInfrastructureObject() *unstructured.Unstructured {
	infra := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupOSConfig + "/v1",
		"kind":       "Infrastructure",
		"metadata": map[string]any{
			"name":              "cluster",
			"creationTimestamp": nil,
		},
		"spec": map[string]any{
			"platformSpec": map[string]any{"type": "None"},
		},
		"status": map[string]any{
			"infrastructureName":     "cloudemu-" + newUID()[:5],
			"controlPlaneTopology":   "HighlyAvailable",
			"infrastructureTopology": "HighlyAvailable",
			"platform":               "None",
			"platformStatus":         map[string]any{"type": "None"},
		},
	}}
	infra.SetUID(types.UID(newUID()))
	infra.SetResourceVersion("1")
	infra.SetGeneration(1)

	return infra
}

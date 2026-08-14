package kubernetes_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// openshiftBase registers an OpenShift-flavored cluster on a fresh httptest
// server and returns the per-cluster base URL (<server>/k8s/<uid>).
func openshiftBase(t *testing.T) string {
	t.Helper()

	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterClusterWithFlavor(kubernetes.FlavorOpenShift)
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	return ts.URL + "/k8s/" + uid
}

// TestOpenShift_ConfigGroupInDiscovery asserts an OpenShift cluster advertises
// config.openshift.io in /apis discovery and lists its singleton kinds under
// the group-version — the surface `oc` negotiates before any config read.
func TestOpenShift_ConfigGroupInDiscovery(t *testing.T) {
	base := openshiftBase(t)

	resp := mustDo(t, http.MethodGet, base+"/apis", nil)
	defer resp.Body.Close()

	var groups struct {
		Groups []struct {
			Name string `json:"name"`
		} `json:"groups"`
	}

	mustDecode(t, resp.Body, &groups)

	if !hasGroup(groups.Groups, "config.openshift.io") {
		t.Fatalf("config.openshift.io not advertised in /apis; groups=%+v", groups.Groups)
	}

	gv := mustDo(t, http.MethodGet, base+"/apis/config.openshift.io/v1", nil)
	defer gv.Body.Close()

	var rl struct {
		Resources []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"resources"`
	}

	mustDecode(t, gv.Body, &rl)

	for _, want := range []string{"clusterversions", "infrastructures"} {
		if !hasResource(rl.Resources, want) {
			t.Fatalf("resource %q missing from config.openshift.io/v1 discovery", want)
		}
	}
}

// TestOpenShift_IdentitySingletonsSeeded asserts a fresh OpenShift cluster boots
// with the ClusterVersion "version" and Infrastructure "cluster" objects, at the
// wire shapes captured from a live OCP cluster.
func TestOpenShift_IdentitySingletonsSeeded(t *testing.T) {
	base := openshiftBase(t)

	cv := mustDo(t, http.MethodGet, base+"/apis/config.openshift.io/v1/clusterversions/version", nil)
	defer cv.Body.Close()

	if cv.StatusCode != http.StatusOK {
		t.Fatalf("GET clusterversion/version: status %d, want 200", cv.StatusCode)
	}

	var cvObj struct {
		Spec struct {
			Channel   string `json:"channel"`
			ClusterID string `json:"clusterID"`
		} `json:"spec"`
		Status struct {
			Desired struct {
				Version string `json:"version"`
			} `json:"desired"`
		} `json:"status"`
	}

	mustDecode(t, cv.Body, &cvObj)

	if cvObj.Spec.Channel != "stable-4.16" {
		t.Errorf("clusterversion channel: got %q, want stable-4.16", cvObj.Spec.Channel)
	}

	if cvObj.Status.Desired.Version != "4.16.0" {
		t.Errorf("clusterversion desired version: got %q, want 4.16.0", cvObj.Status.Desired.Version)
	}

	if cvObj.Spec.ClusterID == "" {
		t.Error("clusterversion spec.clusterID is empty")
	}

	infra := mustDo(t, http.MethodGet, base+"/apis/config.openshift.io/v1/infrastructures/cluster", nil)
	defer infra.Body.Close()

	if infra.StatusCode != http.StatusOK {
		t.Fatalf("GET infrastructure/cluster: status %d, want 200", infra.StatusCode)
	}

	var infraObj struct {
		Status struct {
			InfrastructureName string `json:"infrastructureName"`
			Platform           string `json:"platform"`
		} `json:"status"`
	}

	mustDecode(t, infra.Body, &infraObj)

	if infraObj.Status.InfrastructureName == "" {
		t.Error("infrastructure status.infrastructureName is empty")
	}
}

// TestKubernetes_FlavorHidesOpenShiftGroups asserts flavor gating: a vanilla
// (EKS/AKS/GKE-style) cluster must NOT advertise or serve the *.openshift.io
// groups, and must not carry the identity singletons.
func TestKubernetes_FlavorHidesOpenShiftGroups(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterCluster() // FlavorKubernetes
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	base := ts.URL + "/k8s/" + uid

	resp := mustDo(t, http.MethodGet, base+"/apis", nil)
	defer resp.Body.Close()

	var groups struct {
		Groups []struct {
			Name string `json:"name"`
		} `json:"groups"`
	}

	mustDecode(t, resp.Body, &groups)

	if hasGroup(groups.Groups, "config.openshift.io") {
		t.Fatal("Kubernetes-flavored cluster leaked config.openshift.io into discovery")
	}

	cv := mustDo(t, http.MethodGet, base+"/apis/config.openshift.io/v1/clusterversions/version", nil)
	defer cv.Body.Close()

	if cv.StatusCode != http.StatusNotFound {
		t.Fatalf("GET clusterversion on Kubernetes cluster: status %d, want 404", cv.StatusCode)
	}
}

// TestOpenShift_AllBaseGroupsAdvertised asserts every base OKD group the PR
// registers surfaces in /apis discovery on an OpenShift cluster.
func TestOpenShift_AllBaseGroupsAdvertised(t *testing.T) {
	base := openshiftBase(t)

	resp := mustDo(t, http.MethodGet, base+"/apis", nil)
	defer resp.Body.Close()

	var groups struct {
		Groups []struct {
			Name string `json:"name"`
		} `json:"groups"`
	}

	mustDecode(t, resp.Body, &groups)

	want := []string{
		"config.openshift.io", "apps.openshift.io", "route.openshift.io",
		"build.openshift.io", "image.openshift.io", "project.openshift.io",
		"user.openshift.io", "oauth.openshift.io", "security.openshift.io",
		"quota.openshift.io", "authorization.openshift.io", "template.openshift.io",
	}

	for _, g := range want {
		if !hasGroup(groups.Groups, g) {
			t.Errorf("group %q not advertised in /apis discovery", g)
		}
	}
}

// TestOpenShift_RouteCRUD round-trips a namespaced Route through the generic
// store: create, get, list.
func TestOpenShift_RouteCRUD(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/route.openshift.io/v1/namespaces/default/routes"

	body := []byte(`{
		"apiVersion":"route.openshift.io/v1","kind":"Route",
		"metadata":{"name":"web"},
		"spec":{"host":"web.example.com","to":{"kind":"Service","name":"web","weight":100},"wildcardPolicy":"None"}
	}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create Route: status %d, want 201", create.StatusCode)
	}

	get := mustDo(t, http.MethodGet, col+"/web", nil)
	defer get.Body.Close()

	if get.StatusCode != http.StatusOK {
		t.Fatalf("get Route: status %d, want 200", get.StatusCode)
	}

	var route struct {
		Spec struct {
			Host string `json:"host"`
		} `json:"spec"`
	}

	mustDecode(t, get.Body, &route)

	if route.Spec.Host != "web.example.com" {
		t.Errorf("route spec.host: got %q, want web.example.com", route.Spec.Host)
	}
}

// TestOpenShift_UserCRUD round-trips a cluster-scoped User.
func TestOpenShift_UserCRUD(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/user.openshift.io/v1/users"

	body := []byte(`{"apiVersion":"user.openshift.io/v1","kind":"User","metadata":{"name":"alice"},"fullName":"Alice"}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create User: status %d, want 201", create.StatusCode)
	}

	get := mustDo(t, http.MethodGet, col+"/alice", nil)
	defer get.Body.Close()

	if get.StatusCode != http.StatusOK {
		t.Fatalf("get User: status %d, want 200", get.StatusCode)
	}
}

// TestOpenShift_RouteAdmission asserts the Route reconcile synthesizes a host
// when none is given and publishes an Admitted status.ingress entry.
func TestOpenShift_RouteAdmission(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/route.openshift.io/v1/namespaces/default/routes"

	// No spec.host — the router must synthesize one.
	body := []byte(`{
		"apiVersion":"route.openshift.io/v1","kind":"Route",
		"metadata":{"name":"api"},
		"spec":{"to":{"kind":"Service","name":"api","weight":100}}
	}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create Route: status %d, want 201", create.StatusCode)
	}

	var route struct {
		Spec struct {
			Host string `json:"host"`
		} `json:"spec"`
		Status struct {
			Ingress []struct {
				Host       string `json:"host"`
				RouterName string `json:"routerName"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"ingress"`
		} `json:"status"`
	}

	mustDecode(t, create.Body, &route)

	wantHost := "api-default." + "apps.cloudemu.local"
	if route.Spec.Host != wantHost {
		t.Errorf("synthesized spec.host: got %q, want %q", route.Spec.Host, wantHost)
	}

	if len(route.Status.Ingress) != 1 {
		t.Fatalf("status.ingress: got %d entries, want 1", len(route.Status.Ingress))
	}

	ing := route.Status.Ingress[0]
	if ing.Host != wantHost {
		t.Errorf("status.ingress[0].host: got %q, want %q", ing.Host, wantHost)
	}

	if ing.RouterName != "default" {
		t.Errorf("status.ingress[0].routerName: got %q, want default", ing.RouterName)
	}

	if len(ing.Conditions) != 1 || ing.Conditions[0].Type != "Admitted" || ing.Conditions[0].Status != "True" {
		t.Errorf("status.ingress[0].conditions: got %+v, want [Admitted=True]", ing.Conditions)
	}
}

// TestOpenShift_ImageStreamStatus asserts the ImageStream reconcile synthesizes
// the integrated-registry repositories at the captured shape.
func TestOpenShift_ImageStreamStatus(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/image.openshift.io/v1/namespaces/default/imagestreams"

	body := []byte(`{"apiVersion":"image.openshift.io/v1","kind":"ImageStream","metadata":{"name":"app"}}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create ImageStream: status %d, want 201", create.StatusCode)
	}

	var is struct {
		Status struct {
			DockerImageRepository       string `json:"dockerImageRepository"`
			PublicDockerImageRepository string `json:"publicDockerImageRepository"`
		} `json:"status"`
	}

	mustDecode(t, create.Body, &is)

	wantInternal := "image-registry.openshift-image-registry.svc:5000/default/app"
	if is.Status.DockerImageRepository != wantInternal {
		t.Errorf("dockerImageRepository: got %q, want %q", is.Status.DockerImageRepository, wantInternal)
	}

	wantPublic := "default-route-openshift-image-registry.apps.cloudemu.local/default/app"
	if is.Status.PublicDockerImageRepository != wantPublic {
		t.Errorf("publicDockerImageRepository: got %q, want %q", is.Status.PublicDockerImageRepository, wantPublic)
	}
}

// TestOpenShift_ProjectAnnotations asserts the Project reconcile stamps the
// openshift.io/sa.scc.* annotations OpenShift's project controller injects.
func TestOpenShift_ProjectAnnotations(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/project.openshift.io/v1/projects"

	body := []byte(`{"apiVersion":"project.openshift.io/v1","kind":"Project","metadata":{"name":"team-a"}}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create Project: status %d, want 201", create.StatusCode)
	}

	var proj struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}

	mustDecode(t, create.Body, &proj)

	for _, key := range []string{
		"openshift.io/sa.scc.uid-range",
		"openshift.io/sa.scc.supplemental-groups",
		"openshift.io/sa.scc.mcs",
	} {
		if proj.Metadata.Annotations[key] == "" {
			t.Errorf("project missing annotation %q; got %+v", key, proj.Metadata.Annotations)
		}
	}
}

// TestOpenShift_DeploymentConfigRollout asserts a DeploymentConfig materializes
// Running Pods and reports rollout status (replicas + latestVersion).
func TestOpenShift_DeploymentConfigRollout(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/apps.openshift.io/v1/namespaces/default/deploymentconfigs"

	body := []byte(`{
		"apiVersion":"apps.openshift.io/v1","kind":"DeploymentConfig",
		"metadata":{"name":"web"},
		"spec":{"replicas":3,"selector":{"app":"web"},
			"template":{"metadata":{"labels":{"app":"web"}},
				"spec":{"containers":[{"name":"web","image":"nginx"}]}}}
	}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create DeploymentConfig: status %d, want 201", create.StatusCode)
	}

	var dc struct {
		Status struct {
			Replicas      int64 `json:"replicas"`
			ReadyReplicas int64 `json:"readyReplicas"`
			LatestVersion int64 `json:"latestVersion"`
		} `json:"status"`
	}

	mustDecode(t, create.Body, &dc)

	if dc.Status.Replicas != 3 {
		t.Errorf("dc status.replicas: got %d, want 3", dc.Status.Replicas)
	}

	if dc.Status.LatestVersion != 1 {
		t.Errorf("dc status.latestVersion: got %d, want 1", dc.Status.LatestVersion)
	}

	// The DC's Pods must exist in the namespace.
	pods := mustDo(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)
	defer pods.Body.Close()

	var podList struct {
		Items []struct {
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}

	mustDecode(t, pods.Body, &podList)

	if len(podList.Items) != 3 {
		t.Fatalf("materialized pods: got %d, want 3", len(podList.Items))
	}

	for i, p := range podList.Items {
		if p.Status.Phase != "Running" {
			t.Errorf("pod[%d] phase: got %q, want Running", i, p.Status.Phase)
		}
	}
}

// TestOpenShift_BuildRunsToComplete asserts a Build reconciles to Complete and
// materializes its builder Pod.
func TestOpenShift_BuildRunsToComplete(t *testing.T) {
	base := openshiftBase(t)
	col := base + "/apis/build.openshift.io/v1/namespaces/default/builds"

	body := []byte(`{"apiVersion":"build.openshift.io/v1","kind":"Build","metadata":{"name":"app-1"},
		"spec":{"strategy":{"type":"Docker"}}}`)

	create := mustDo(t, http.MethodPost, col, body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create Build: status %d, want 201", create.StatusCode)
	}

	var build struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}

	mustDecode(t, create.Body, &build)

	if build.Status.Phase != "Complete" {
		t.Errorf("build status.phase: got %q, want Complete", build.Status.Phase)
	}

	pods := mustDo(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/app-1-build", nil)
	defer pods.Body.Close()

	if pods.StatusCode != http.StatusOK {
		t.Fatalf("builder pod app-1-build: status %d, want 200", pods.StatusCode)
	}
}

// TestOpenShift_StartBuild asserts `oc start-build` (BuildConfig instantiate)
// mints a Build named <bc>-<n> that runs to completion.
func TestOpenShift_StartBuild(t *testing.T) {
	base := openshiftBase(t)

	bc := []byte(`{"apiVersion":"build.openshift.io/v1","kind":"BuildConfig","metadata":{"name":"api"},
		"spec":{"strategy":{"type":"Source"}}}`)

	mkBC := mustDo(t, http.MethodPost, base+"/apis/build.openshift.io/v1/namespaces/default/buildconfigs", bc)
	if mkBC.StatusCode != http.StatusCreated {
		mkBC.Body.Close()
		t.Fatalf("create BuildConfig: status %d, want 201", mkBC.StatusCode)
	}

	mkBC.Body.Close()

	inst := mustDo(t, http.MethodPost,
		base+"/apis/build.openshift.io/v1/namespaces/default/buildconfigs/api/instantiate", []byte(`{}`))
	defer inst.Body.Close()

	if inst.StatusCode != http.StatusCreated {
		t.Fatalf("start-build: status %d, want 201", inst.StatusCode)
	}

	var build struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}

	mustDecode(t, inst.Body, &build)

	if build.Metadata.Name != "api-1" {
		t.Errorf("started build name: got %q, want api-1", build.Metadata.Name)
	}

	if build.Status.Phase != "Complete" {
		t.Errorf("started build phase: got %q, want Complete", build.Status.Phase)
	}
}

// TestOpenShift_NewProject asserts `oc new-project` (ProjectRequest) creates
// both the Project and its backing Namespace.
func TestOpenShift_NewProject(t *testing.T) {
	base := openshiftBase(t)

	body := []byte(`{"apiVersion":"project.openshift.io/v1","kind":"ProjectRequest",
		"metadata":{"name":"team-x"},"displayName":"Team X","description":"x team"}`)

	create := mustDo(t, http.MethodPost, base+"/apis/project.openshift.io/v1/projectrequests", body)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("projectrequest: status %d, want 201", create.StatusCode)
	}

	var proj struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}

	mustDecode(t, create.Body, &proj)

	if proj.Metadata.Name != "team-x" {
		t.Errorf("project name: got %q, want team-x", proj.Metadata.Name)
	}

	if proj.Metadata.Annotations["openshift.io/display-name"] != "Team X" {
		t.Errorf("display-name annotation: got %q, want Team X", proj.Metadata.Annotations["openshift.io/display-name"])
	}

	if proj.Metadata.Annotations["openshift.io/sa.scc.uid-range"] == "" {
		t.Error("project missing sa.scc.uid-range annotation from reconcile")
	}

	// The backing namespace must exist (so the project is usable).
	ns := mustDo(t, http.MethodGet, base+"/api/v1/namespaces/team-x", nil)
	defer ns.Body.Close()

	if ns.StatusCode != http.StatusOK {
		t.Fatalf("backing namespace team-x: status %d, want 200", ns.StatusCode)
	}
}

// TestOpenShift_ProjectRequestsGET asserts the projectrequests collection
// answers GET with an empty list — the probe `oc new-project` issues before it
// POSTs. Without it the GET 404s and the CLI command aborts.
func TestOpenShift_ProjectRequestsGET(t *testing.T) {
	base := openshiftBase(t)

	resp := mustDo(t, http.MethodGet, base+"/apis/project.openshift.io/v1/projectrequests", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET projectrequests: status %d, want 200", resp.StatusCode)
	}

	var list struct {
		Kind  string `json:"kind"`
		Items []any  `json:"items"`
	}

	mustDecode(t, resp.Body, &list)

	if list.Kind != "ProjectRequestList" {
		t.Errorf("kind: got %q, want ProjectRequestList", list.Kind)
	}
}

// TestOpenShift_ProcessTemplate asserts `oc process` (processedtemplates) fills
// parameters (provided + generated) and substitutes ${PARAM} into the objects.
func TestOpenShift_ProcessTemplate(t *testing.T) {
	base := openshiftBase(t)

	body := []byte(`{
		"apiVersion":"template.openshift.io/v1","kind":"Template","metadata":{"name":"tmpl"},
		"parameters":[
			{"name":"NAME","value":"myapp"},
			{"name":"SECRET","generate":"expression","from":"[a-z0-9]{12}"}
		],
		"objects":[
			{"apiVersion":"v1","kind":"Service","metadata":{"name":"${NAME}"},
			 "data":{"token":"${SECRET}"}}
		]
	}`)

	resp := mustDo(t, http.MethodPost,
		base+"/apis/template.openshift.io/v1/namespaces/default/processedtemplates", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("processedtemplates: status %d, want 200", resp.StatusCode)
	}

	var processed struct {
		Parameters []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"parameters"`
		Objects []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		} `json:"objects"`
	}

	mustDecode(t, resp.Body, &processed)

	if len(processed.Objects) != 1 {
		t.Fatalf("processed objects: got %d, want 1", len(processed.Objects))
	}

	if processed.Objects[0].Metadata.Name != "myapp" {
		t.Errorf("${NAME} not substituted: got %q, want myapp", processed.Objects[0].Metadata.Name)
	}

	var secret string

	for _, p := range processed.Parameters {
		if p.Name == "SECRET" {
			secret = p.Value
		}
	}

	if len(secret) != 12 {
		t.Errorf("generated SECRET length: got %d (%q), want 12", len(secret), secret)
	}

	if processed.Objects[0].Data.Token != secret {
		t.Errorf("${SECRET} substitution mismatch: object=%q param=%q",
			processed.Objects[0].Data.Token, secret)
	}
}

// TestOpenShift_RemainingGroupsAdvertised asserts the console/operator/machine/
// autoscaling groups surface in discovery and round-trip a representative kind.
func TestOpenShift_RemainingGroupsAdvertised(t *testing.T) {
	base := openshiftBase(t)

	resp := mustDo(t, http.MethodGet, base+"/apis", nil)
	defer resp.Body.Close()

	var groups struct {
		Groups []struct {
			Name string `json:"name"`
		} `json:"groups"`
	}

	mustDecode(t, resp.Body, &groups)

	for _, g := range []string{
		"console.openshift.io", "operator.openshift.io",
		"machine.openshift.io", "autoscaling.openshift.io",
	} {
		if !hasGroup(groups.Groups, g) {
			t.Errorf("group %q not advertised", g)
		}
	}

	// A cluster-scoped ConsoleLink round-trips.
	link := []byte(`{"apiVersion":"console.openshift.io/v1","kind":"ConsoleLink",
		"metadata":{"name":"docs"},"spec":{"href":"https://docs","location":"HelpMenu","text":"Docs"}}`)

	create := mustDo(t, http.MethodPost, base+"/apis/console.openshift.io/v1/consolelinks", link)
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create ConsoleLink: status %d, want 201", create.StatusCode)
	}
}

// TestOpenShift_OAuthMetadata asserts the OAuth well-known document advertises
// absolute endpoints rooted at the cluster's own URL.
func TestOpenShift_OAuthMetadata(t *testing.T) {
	base := openshiftBase(t)

	resp := mustDo(t, http.MethodGet, base+"/.well-known/oauth-authorization-server", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("well-known: status %d, want 200", resp.StatusCode)
	}

	var md struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}

	mustDecode(t, resp.Body, &md)

	if md.Issuer != base {
		t.Errorf("issuer: got %q, want %q", md.Issuer, base)
	}

	if md.AuthorizationEndpoint != base+"/oauth/authorize" {
		t.Errorf("authorization_endpoint: got %q, want %q", md.AuthorizationEndpoint, base+"/oauth/authorize")
	}

	if md.TokenEndpoint != base+"/oauth/token" {
		t.Errorf("token_endpoint: got %q, want %q", md.TokenEndpoint, base+"/oauth/token")
	}
}

// TestOpenShift_OAuthRejectsCrossHostRedirect asserts the authorize endpoint
// refuses a cross-origin redirect_uri (open-redirect / token-exfiltration
// guard) rather than 302-ing the token to an attacker-controlled host.
func TestOpenShift_OAuthRejectsCrossHostRedirect(t *testing.T) {
	base := openshiftBase(t)
	authorize := base + "/oauth/authorize?client_id=openshift-challenging-client&response_type=token&redirect_uri=" +
		url.QueryEscape("https://evil.example.com/steal")

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, _ := http.NewRequest(http.MethodGet, authorize, nil)
	req.SetBasicAuth("developer", "x")

	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("authorize request: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-host redirect_uri: status %d, want 400 (must not redirect)", resp.StatusCode)
	}

	if loc := resp.Header.Get("Location"); strings.Contains(loc, "evil.example.com") {
		t.Errorf("server redirected to attacker host: %q", loc)
	}
}

// TestOpenShift_OAuthChallengeThenToken asserts the challenging-client flow:
// no credentials -> 401 Basic challenge; Basic credentials -> 302 with the
// access token in the redirect fragment; and that token then resolves via
// whoami to the authenticated user.
func TestOpenShift_OAuthChallengeThenToken(t *testing.T) {
	base := openshiftBase(t)
	// redirect_uri must be same-host (the server rejects cross-origin to prevent
	// an open redirect) — exactly what the real oc challenging-client sends.
	authorize := base + "/oauth/authorize?client_id=openshift-challenging-client&response_type=token&redirect_uri=" +
		url.QueryEscape(base+"/oauth/token/implicit")

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// No credentials -> challenge.
	chReq, _ := http.NewRequest(http.MethodGet, authorize, nil)
	chResp, err := noRedirect.Do(chReq)
	if err != nil {
		t.Fatalf("challenge request: %v", err)
	}

	chResp.Body.Close()

	if chResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated authorize: status %d, want 401", chResp.StatusCode)
	}

	if !strings.HasPrefix(chResp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Errorf("missing Basic challenge; WWW-Authenticate=%q", chResp.Header.Get("WWW-Authenticate"))
	}

	// With Basic credentials -> 302 with token in fragment.
	req, _ := http.NewRequest(http.MethodGet, authorize, nil)
	req.SetBasicAuth("developer", "any-password")

	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("authorize request: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authenticated authorize: status %d, want 302", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")

	frag := ""
	if i := strings.Index(loc, "#"); i >= 0 {
		frag = loc[i+1:]
	}

	token := valueFromEncoded(frag, "access_token")
	if token == "" {
		t.Fatalf("no access_token in redirect fragment: %q", loc)
	}

	// The token resolves via whoami to the authenticated user.
	who, _ := http.NewRequest(http.MethodGet, base+"/apis/user.openshift.io/v1/users/~", nil)
	who.Header.Set("Authorization", "Bearer "+token)

	whoResp, err := http.DefaultClient.Do(who)
	if err != nil {
		t.Fatalf("whoami request: %v", err)
	}

	defer whoResp.Body.Close()

	var user struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}

	mustDecode(t, whoResp.Body, &user)

	if user.Metadata.Name != "developer" {
		t.Errorf("whoami: got %q, want developer", user.Metadata.Name)
	}
}

// valueFromEncoded pulls key's value out of a urlencoded fragment/query string
// without depending on ordering.
func valueFromEncoded(encoded, key string) string {
	for _, kv := range strings.Split(encoded, "&") {
		k, v, found := strings.Cut(kv, "=")
		if found && k == key {
			return v
		}
	}

	return ""
}

func hasGroup(groups []struct {
	Name string `json:"name"`
}, name string) bool {
	for _, g := range groups {
		if g.Name == name {
			return true
		}
	}

	return false
}

func hasResource(res []struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}, name string) bool {
	for _, r := range res {
		if r.Name == name {
			return true
		}
	}

	return false
}

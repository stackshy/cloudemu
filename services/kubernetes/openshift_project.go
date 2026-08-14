package kubernetes

import (
	"net/http"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// projectRequestPath is the POST-only `oc new-project` verb. A ProjectRequest is
// not a stored kind — posting one provisions a namespace and its paired Project.
const projectRequestPath = "/apis/project.openshift.io/v1/projectrequests"

// serveProjectRequestList answers GET on the projectrequests collection with an
// empty ProjectRequestList. `oc new-project` issues this GET before its POST to
// check whether the caller may request projects; on this unauthenticated backend
// anyone may, so an empty list (HTTP 200) lets the command proceed to the POST.
func serveProjectRequestList(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": apiGroupOSProject + "/v1",
		"kind":       "ProjectRequestList",
		"metadata":   map[string]any{},
		"items":      []any{},
	})
}

// serveProjectRequest implements `oc new-project`: it creates the backing
// Namespace (the real tenancy boundary) and the paired Project object, then
// returns the Project. On a real cluster the project controller creates the
// Namespace from the request; the emulator does both inline so the new project
// is immediately usable for `oc apply`.
func (s *ClusterState) serveProjectRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	name := req.Metadata.Name
	if name == "" {
		writeBadRequest(w, "openshift: projectrequest metadata.name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.reg.getStore(apiGroupOSProject, "v1", "projects")
	if st == nil {
		writeNotFound(w, "openshift: project store unavailable")

		return
	}

	if _, exists := st.items[objKey("", name)]; exists {
		writeAlreadyExists(w, "openshift: project already exists: "+name)

		return
	}

	s.ensureNamespaceLocked(name)

	proj := newProjectObject(name, req.DisplayName, req.Description)
	proj.SetCreationTimestamp(s.now())
	reconcileProject(s, proj)
	st.stampRVLocked(proj)
	st.items[objKey("", name)] = proj
	st.watch.publish(EventAdded, "", *proj.DeepCopy())

	writeJSON(w, http.StatusCreated, proj)
}

// ensureNamespaceLocked creates the namespace (and its default ServiceAccount)
// if absent, mirroring createNamespace's bootstrap. Callers hold s.mu.
func (s *ClusterState) ensureNamespaceLocked(name string) {
	if _, ok := s.namespaces[name]; ok {
		return
	}

	ns := s.newNamespaceObject(name)
	s.namespaces[name] = ns

	sa := s.newServiceAccountObject(name, "default")
	s.serviceAccounts[serviceAccountKey(name, "default")] = sa

	s.wNamespaces.publish(EventAdded, "", *ns.DeepCopy())
	s.wServiceAccounts.publish(EventAdded, name, *sa.DeepCopy())
}

// newProjectObject builds a project.openshift.io/v1 Project. displayName and
// description, when set, become the openshift.io/{display-name,description}
// annotations a real ProjectRequest records.
func newProjectObject(name, displayName, description string) *unstructured.Unstructured {
	annotations := map[string]any{}
	if displayName != "" {
		annotations["openshift.io/display-name"] = displayName
	}

	if description != "" {
		annotations["openshift.io/description"] = description
	}

	proj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupOSProject + "/v1",
		"kind":       "Project",
		"metadata": map[string]any{
			"name":        name,
			"annotations": annotations,
		},
		"status": map[string]any{"phase": "Active"},
	}}
	proj.SetUID(types.UID(newUID()))
	proj.SetGeneration(1)

	return proj
}

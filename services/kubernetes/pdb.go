package kubernetes

import (
	"net/http"
	"sort"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// apiGroupPolicy is the API group PodDisruptionBudget lives under:
// /apis/policy/v1/...
const apiGroupPolicy = "policy"

// servePDBs dispatches /apis/policy/v1/.../poddisruptionbudgets.
//
// PDBs are here because real Helm charts create them as a matter of course —
// a chart that renders a Deployment and a Service almost always renders a PDB
// alongside. Without the resource, `helm install` fails at object-building
// with "no matches for kind PodDisruptionBudget", which stops the release
// before any of the workload resources the emulator DOES support are reached.
// Supporting the workload kinds but not the PDB that ships with them means a
// realistic chart still cannot be installed.
func (s *ClusterState) servePDBs(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != apiGroupPolicy || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: poddisruptionbudgets are only served at /apis/policy/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: poddisruptionbudgets cluster-wide: method not allowed: "+r.Method)

			return
		}

		s.listPDBs(w, "")

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.servePDBCollection(w, r, route)

		return
	}

	s.servePDBItem(w, r, route)
}

func (s *ClusterState) servePDBCollection(w http.ResponseWriter, r *http.Request, route *Route) {
	switch r.Method {
	case http.MethodGet:
		s.listPDBs(w, route.Namespace)
	case http.MethodPost:
		s.createPDB(w, r, route)
	default:
		writeMethodNotAllowed(w, "k8s api: poddisruptionbudgets collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) servePDBItem(w http.ResponseWriter, r *http.Request, route *Route) {
	switch r.Method {
	case http.MethodGet:
		s.getPDB(w, route)
	case http.MethodPut:
		s.replacePDB(w, r, route)
	case http.MethodDelete:
		s.deletePDB(w, route)
	default:
		writeMethodNotAllowed(w, "k8s api: poddisruptionbudget: method not allowed: "+r.Method)
	}
}

func pdbKey(namespace, name string) string { return namespace + "/" + name }

func (s *ClusterState) createPDB(w http.ResponseWriter, r *http.Request, route *Route) {
	var in policyv1.PodDisruptionBudget
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: poddisruptionbudget name is required")

		return
	}

	in.Namespace = route.Namespace

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.pdbs[pdbKey(in.Namespace, in.Name)]; exists {
		writeAlreadyExists(w, "k8s api: poddisruptionbudget already exists: "+pdbKey(in.Namespace, in.Name))

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "PodDisruptionBudget", APIVersion: "policy/v1"}

	// Real PDB status is computed by the disruption controller from live pods.
	// There is no controller here, so report the shape a client expects without
	// inventing eviction semantics the emulator cannot honour.
	in.Status = policyv1.PodDisruptionBudgetStatus{ObservedGeneration: 1}

	s.pdbs[pdbKey(in.Namespace, in.Name)] = &in

	writeJSON(w, http.StatusCreated, &in)
}

func (s *ClusterState) getPDB(w http.ResponseWriter, route *Route) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pdb, ok := s.pdbs[pdbKey(route.Namespace, route.Name)]
	if !ok {
		writeNotFound(w, "k8s api: poddisruptionbudget not found: "+route.Name)

		return
	}

	writeJSON(w, http.StatusOK, pdb)
}

func (s *ClusterState) replacePDB(w http.ResponseWriter, r *http.Request, route *Route) {
	var in policyv1.PodDisruptionBudget
	if !readJSON(w, r, &in) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := pdbKey(route.Namespace, route.Name)

	existing, ok := s.pdbs[key]
	if !ok {
		writeNotFound(w, "k8s api: poddisruptionbudget not found: "+route.Name)

		return
	}

	in.Namespace, in.Name = route.Namespace, route.Name
	in.CreationTimestamp = existing.CreationTimestamp
	in.UID = existing.UID
	in.ResourceVersion = bumpResourceVersion(existing.ResourceVersion)
	in.TypeMeta = metav1.TypeMeta{Kind: "PodDisruptionBudget", APIVersion: "policy/v1"}
	in.Status = existing.Status

	s.pdbs[key] = &in

	writeJSON(w, http.StatusOK, &in)
}

func (s *ClusterState) deletePDB(w http.ResponseWriter, route *Route) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := pdbKey(route.Namespace, route.Name)
	if _, ok := s.pdbs[key]; !ok {
		writeNotFound(w, "k8s api: poddisruptionbudget not found: "+route.Name)

		return
	}

	delete(s.pdbs, key)

	writeJSON(w, http.StatusOK, &metav1.Status{Status: metav1.StatusSuccess})
}

// listPDBs lists one namespace, or every namespace when namespace is "".
func (s *ClusterState) listPDBs(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]policyv1.PodDisruptionBudget, 0, len(s.pdbs))

	for _, pdb := range s.pdbs {
		if namespace == "" || pdb.Namespace == namespace {
			items = append(items, *pdb)
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}

		return items[i].Name < items[j].Name
	})

	writeJSON(w, http.StatusOK, &policyv1.PodDisruptionBudgetList{
		TypeMeta: metav1.TypeMeta{APIVersion: "policy/v1", Kind: "PodDisruptionBudgetList"},
		Items:    items,
	})
}

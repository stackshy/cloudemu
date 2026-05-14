package kubernetes

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// clusterIPNone is the sentinel value a client sets on a headless Service —
// real apiserver leaves the ClusterIP empty in that case.
const clusterIPNone = "None"

// serveServices dispatches /api/v1/{namespaces/{ns}/services|services}
// requests.
//
// Per-resource files share the dispatch shape on purpose; each resource keeps
// its quirks (Service ClusterIP, Secret StringData merge) close to its type.
//
//nolint:dupl // see comment above.
func (s *ClusterState) serveServices(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: services are only served at /api/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: services cluster-wide: method not allowed: "+r.Method)

			return
		}

		if r.URL.Query().Get("watch") == "true" {
			s.watchServices(w, r, "")

			return
		}

		s.listServicesAllNamespaces(w)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.serveServiceCollection(w, r, route.Namespace)

		return
	}

	s.serveServiceItem(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) serveServiceCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("watch") == "true" {
			s.watchServices(w, r, namespace)

			return
		}

		s.listServices(w, namespace)
	case http.MethodPost:
		s.createService(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: services collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) watchServices(w http.ResponseWriter, r *http.Request, namespace string) {
	s.mu.RLock()
	items := s.collectServicesLocked(namespace)
	s.mu.RUnlock()
	streamWatch(r.Context(), w, s.wServices, namespace, items)
}

func (s *ClusterState) serveServiceItem(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getService(w, namespace, name)
	case http.MethodPut:
		s.updateService(w, r, namespace, name)
	case http.MethodPatch:
		s.patchService(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteService(w, namespace, name)
	default:
		writeMethodNotAllowed(w, "k8s api: service item: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) createService(w http.ResponseWriter, r *http.Request, namespace string) {
	var in corev1.Service
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: service: metadata.name is required")

		return
	}

	in.Namespace = namespace

	key := serviceKey(namespace, in.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.services[key]; ok {
		writeAlreadyExists(w, "k8s api: service already exists: "+key)

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "Service", APIVersion: "v1"}

	if in.Spec.Type == "" {
		in.Spec.Type = corev1.ServiceTypeClusterIP
	}

	// ClusterIP allocation. "" → allocate next sequential IP. "None" →
	// headless: keep as-is and leave ClusterIPs nil. Anything else → accept
	// verbatim (real apiserver validates conflicts; a test backend doesn't
	// need to).
	if in.Spec.ClusterIP == "" {
		in.Spec.ClusterIP = s.allocateClusterIPLocked()
	}

	if len(in.Spec.ClusterIPs) == 0 && in.Spec.ClusterIP != "" && in.Spec.ClusterIP != clusterIPNone {
		in.Spec.ClusterIPs = []string{in.Spec.ClusterIP}
	}

	svc := in
	s.services[key] = &svc
	s.wServices.publish(EventAdded, namespace, *svc.DeepCopy())
	writeJSON(w, http.StatusCreated, &svc)
}

func (s *ClusterState) listServices(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectServicesLocked(namespace)
	writeJSON(w, http.StatusOK, &corev1.ServiceList{
		TypeMeta: metav1.TypeMeta{Kind: "ServiceList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) listServicesAllNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectServicesLocked("")
	writeJSON(w, http.StatusOK, &corev1.ServiceList{
		TypeMeta: metav1.TypeMeta{Kind: "ServiceList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) collectServicesLocked(namespace string) []corev1.Service {
	keys := make([]string, 0, len(s.services))

	for k := range s.services {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]corev1.Service, 0, len(keys))
	for _, k := range keys {
		items = append(items, *s.services[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getService(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.services[serviceKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: service not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, svc.DeepCopy())
}

func (s *ClusterState) updateService(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var in corev1.Service
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name != name {
		writeBadRequest(w, "k8s api: service name in body does not match URL")

		return
	}

	key := serviceKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.services[key]
	if !ok {
		writeNotFound(w, "k8s api: service not found: "+key)

		return
	}

	in.Namespace = namespace
	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	// ClusterIP is immutable after Create (kubernetes apiserver semantics).
	// Carry the allocated IP forward regardless of what the client sends.
	in.Spec.ClusterIP = cur.Spec.ClusterIP
	in.Spec.ClusterIPs = cur.Spec.ClusterIPs

	// Real apiserver requires .spec.type to remain set after the first
	// successful create. If the client omits it, fall back to the stored
	// value rather than silently corrupting the object.
	if in.Spec.Type == "" {
		in.Spec.Type = cur.Spec.Type
	}

	svc := in
	s.services[key] = &svc
	s.wServices.publish(EventModified, namespace, *svc.DeepCopy())
	writeJSON(w, http.StatusOK, &svc)
}

func (s *ClusterState) patchService(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := serviceKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.services[key]
	if !ok {
		writeNotFound(w, "k8s api: service not found: "+key)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	// Same ClusterIP-immutable rule as updateService.
	patched.Spec.ClusterIP = cur.Spec.ClusterIP
	patched.Spec.ClusterIPs = cur.Spec.ClusterIPs
	s.services[key] = patched
	s.wServices.publish(EventModified, namespace, *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deleteService(w http.ResponseWriter, namespace, name string) {
	key := serviceKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	svc, ok := s.services[key]
	if !ok {
		writeNotFound(w, "k8s api: service not found: "+key)

		return
	}

	delete(s.services, key)
	s.wServices.publish(EventDeleted, namespace, *svc.DeepCopy())
	writeJSON(w, http.StatusOK, svc.DeepCopy())
}

func serviceKey(namespace, name string) string {
	return namespace + "/" + name
}

// allocateClusterIPLocked hands out the next sequential ClusterIP from
// 10.96.0.0/12. Caller must hold s.mu (write lock).
//
// Real apiserver allocates from a per-cluster CIDR bitmap and reclaims IPs
// on Service deletion. Tests don't typically reuse IPs, so we use a simple
// monotonic counter that never reclaims; the 10.96.0.0/12 range gives us
// over a million IPs before wrap-around, which is more than enough.
func (s *ClusterState) allocateClusterIPLocked() string {
	offset := s.nextClusterIP
	s.nextClusterIP++

	// 10.96.0.0 + offset rendered as a dotted-quad. Only the lower 20 bits
	// are interesting for a /12; mask just in case to avoid garbage values
	// in the unlikely event of overflow.
	const (
		baseAddr        uint32 = 10<<24 | 96<<16
		octetMask       uint32 = 0xFF
		serviceCIDRMask uint32 = 0x000FFFFF // /12 — host bits below the network
		shift1                 = 24
		shift2                 = 16
		shift3                 = 8
	)

	addr := baseAddr + (offset & serviceCIDRMask)

	return fmt.Sprintf("%d.%d.%d.%d",
		(addr>>shift1)&octetMask,
		(addr>>shift2)&octetMask,
		(addr>>shift3)&octetMask,
		addr&octetMask,
	)
}

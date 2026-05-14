package kubernetes

import (
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// serveSecrets dispatches /api/v1/{namespaces/{ns}/secrets|secrets} requests.
//
// Per-resource files share the dispatch shape on purpose; each resource keeps
// its quirks (Service ClusterIP, Secret StringData merge) close to its type.
//
//nolint:dupl // see comment above.
func (s *ClusterState) serveSecrets(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: secrets are only served at /api/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: secrets cluster-wide: method not allowed: "+r.Method)

			return
		}

		s.listSecretsAllNamespaces(w)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.serveSecretCollection(w, r, route.Namespace)

		return
	}

	s.serveSecretItem(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) serveSecretCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	switch r.Method {
	case http.MethodGet:
		s.listSecrets(w, namespace)
	case http.MethodPost:
		s.createSecret(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: secrets collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) serveSecretItem(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getSecret(w, namespace, name)
	case http.MethodPut:
		s.updateSecret(w, r, namespace, name)
	case http.MethodPatch:
		s.patchSecret(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteSecret(w, namespace, name)
	default:
		writeMethodNotAllowed(w, "k8s api: secret item: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) createSecret(w http.ResponseWriter, r *http.Request, namespace string) {
	var in corev1.Secret
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: secret: metadata.name is required")

		return
	}

	in.Namespace = namespace

	mergeStringData(&in)

	key := secretKey(namespace, in.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.secrets[key]; ok {
		writeAlreadyExists(w, "k8s api: secret already exists: "+key)

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"}

	if in.Type == "" {
		in.Type = corev1.SecretTypeOpaque
	}

	sec := in
	s.secrets[key] = &sec
	writeJSON(w, http.StatusCreated, &sec)
}

func (s *ClusterState) listSecrets(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectSecretsLocked(namespace)
	writeJSON(w, http.StatusOK, &corev1.SecretList{
		TypeMeta: metav1.TypeMeta{Kind: "SecretList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) listSecretsAllNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectSecretsLocked("")
	writeJSON(w, http.StatusOK, &corev1.SecretList{
		TypeMeta: metav1.TypeMeta{Kind: "SecretList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) collectSecretsLocked(namespace string) []corev1.Secret {
	keys := make([]string, 0, len(s.secrets))

	for k := range s.secrets {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]corev1.Secret, 0, len(keys))
	for _, k := range keys {
		items = append(items, *s.secrets[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getSecret(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sec, ok := s.secrets[secretKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: secret not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, sec.DeepCopy())
}

func (s *ClusterState) updateSecret(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var in corev1.Secret
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name != name {
		writeBadRequest(w, "k8s api: secret name in body does not match URL")

		return
	}

	mergeStringData(&in)

	key := secretKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.secrets[key]
	if !ok {
		writeNotFound(w, "k8s api: secret not found: "+key)

		return
	}

	in.Namespace = namespace
	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	if in.Type == "" {
		in.Type = cur.Type
	}

	sec := in
	s.secrets[key] = &sec
	writeJSON(w, http.StatusOK, &sec)
}

// Patch flow is identical across namespaced resources; sharing would force a
// runtime type-switch and obscure the resource-specific store access.
//
//nolint:dupl // see comment above.
func (s *ClusterState) patchSecret(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := secretKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.secrets[key]
	if !ok {
		writeNotFound(w, "k8s api: secret not found: "+key)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	s.secrets[key] = patched
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deleteSecret(w http.ResponseWriter, namespace, name string) {
	key := secretKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	sec, ok := s.secrets[key]
	if !ok {
		writeNotFound(w, "k8s api: secret not found: "+key)

		return
	}

	delete(s.secrets, key)
	writeJSON(w, http.StatusOK, sec.DeepCopy())
}

func secretKey(namespace, name string) string {
	return namespace + "/" + name
}

// mergeStringData implements the apiserver's convenience-field behavior: any
// keys in StringData are byte-encoded and copied into Data, overwriting on
// conflict. StringData is then cleared from the persisted object — clients
// reading the Secret back see everything in Data, base64-encoded on the
// wire by encoding/json's default []byte handling.
func mergeStringData(sec *corev1.Secret) {
	if len(sec.StringData) == 0 {
		return
	}

	if sec.Data == nil {
		sec.Data = make(map[string][]byte, len(sec.StringData))
	}

	for k, v := range sec.StringData {
		sec.Data[k] = []byte(v)
	}

	sec.StringData = nil
}

package kubernetes

import "net/http"

// serveSubresource dispatches requests for object subresources (/status,
// /scale). It is populated in the reconcile phase; until a kind advertises a
// given subresource it answers 404, matching a real apiserver's response for a
// subresource that doesn't exist.
func (s *ClusterState) serveSubresource(w http.ResponseWriter, r *http.Request, route *Route) {
	writeNotFound(w, "k8s api: subresource not implemented: "+route.Resource+"/"+route.Name+"/"+route.Subresource)
}

package ocm

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/providers/openshift/ocm"
)

// serveToken mints an SSO access token for any credentials, so `rosa login`
// (client-credentials or offline-token grant) succeeds against the emulator.
func (*Handler) serveToken(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: "cloudemu-ocm-" + idgen.GenerateID(""),
		TokenType:   "Bearer",
		ExpiresIn:   900,
		Scope:       "openid",
	})
}

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string   `json:"name"`
		Region        *ocmLink `json:"region"`
		CloudProvider *ocmLink `json:"cloud_provider"`
		Version       *ocmLink `json:"version"`
		Product       *ocmLink `json:"product"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	in := ocm.ClusterInput{Name: body.Name}
	if body.Region != nil {
		in.Region = body.Region.ID
	}

	if body.CloudProvider != nil {
		in.CloudProvider = body.CloudProvider.ID
	}

	if body.Product != nil {
		in.Product = body.Product.ID
	}

	if body.Version != nil {
		in.Version = normalizeVersion(body.Version.ID)
	}

	cluster, err := h.be.CreateCluster(r.Context(), in)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, toOCMCluster(cluster))
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, id string) {
	cluster, err := h.be.GetCluster(r.Context(), id)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, toOCMCluster(cluster))
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request) {
	clusters := h.be.ListClusters(r.Context())

	items := make([]ocmCluster, 0, len(clusters))
	for i := range clusters {
		items = append(items, toOCMCluster(&clusters[i]))
	}

	writeJSON(w, http.StatusOK, ocmClusterList{
		Kind: "ClusterList", Page: 1, Size: len(items), Total: len(items), Items: items,
	})
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.be.DeleteCluster(r.Context(), id); err != nil {
		writeCErr(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getCredentials(w http.ResponseWriter, _ *http.Request, id string) {
	kubeconfig, err := h.be.Kubeconfig(id)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, ocmCredentials{Kind: "Credentials", Kubeconfig: string(kubeconfig)})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeOCMError(w, http.StatusBadRequest, "400", "invalid request body: "+err.Error())

		return false
	}

	return true
}

func writeOCMError(w http.ResponseWriter, status int, code, reason string) {
	writeJSON(w, status, ocmError{Kind: "Error", ID: code, Code: "CLUSTERS-MGMT-" + code, Reason: reason})
}

// writeCErr maps a cloudemu error to the OCM error envelope + HTTP status.
func writeCErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	switch {
	case cerrors.IsNotFound(err):
		status = http.StatusNotFound
	case cerrors.IsInvalidArgument(err):
		status = http.StatusBadRequest
	}

	writeOCMError(w, status, http.StatusText(status), err.Error())
}

// normalizeVersion strips OCM's "openshift-v" version-id prefix to a bare
// semver ("openshift-v4.16.0" -> "4.16.0"), leaving bare versions unchanged.
func normalizeVersion(id string) string {
	const prefix = "openshift-v"
	if len(id) > len(prefix) && id[:len(prefix)] == prefix {
		return id[len(prefix):]
	}

	return id
}

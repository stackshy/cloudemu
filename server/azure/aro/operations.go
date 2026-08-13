package aro

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/providers/azure/aro"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) createOrUpdateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armOpenShiftCluster
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	in := aro.ClusterInput{
		Subscription:  rp.Subscription,
		ResourceGroup: rp.ResourceGroup,
		Name:          rp.ResourceName,
		Location:      body.Location,
		Tags:          fromPtrTags(body.Tags),
	}
	if body.Properties != nil && body.Properties.ClusterProfile != nil {
		in.Version = body.Properties.ClusterProfile.Version
	}

	cluster, err := h.be.CreateOrUpdateCluster(r.Context(), in)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(cluster))
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cluster, err := h.be.GetCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(cluster))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.be.DeleteCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var clusters []aro.OpenShiftCluster
	if rp.ResourceGroup == "" {
		clusters = h.be.ListClusters(r.Context(), rp.Subscription)
	} else {
		clusters = h.be.ListClustersByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	}

	out := make([]armOpenShiftCluster, 0, len(clusters))
	for i := range clusters {
		out = append(out, toARMCluster(&clusters[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armOpenShiftCluster]{Value: out})
}

// listAdminCredentials returns the admin kubeconfig (base64-encoded by the
// []byte field), pointing at the cluster's OpenShift data plane when wired.
func (h *Handler) listAdminCredentials(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.be.GetCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	kubeconfig := h.be.Kubeconfig(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, armAdminKubeconfig{Kubeconfig: kubeconfig})
}

// listCredentials returns the kubeadmin username/password. cloudemu is
// unauthenticated, so the password is a stable placeholder — the credential
// exists so `az aro list-credentials` round-trips.
func (h *Handler) listCredentials(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.be.GetCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, armCredentials{
		KubeadminUsername: "kubeadmin",
		KubeadminPassword: "cloudemu-kubeadmin",
	})
}

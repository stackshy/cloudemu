package vault

import (
	"encoding/base64"
	"net/http"
	"strconv"

	vaultprovider "github.com/stackshy/cloudemu/v2/providers/oci/vault"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveBundles routes the secret-retrieval data plane, which reads secret
// values and nothing else. It takes no compartmentId anywhere: a bundle is
// addressed by secret OCID, or by vault and secret name.
func (h *Handler) serveBundles(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.seg(idxCollection) != segSecretBundles {
		notFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	switch {
	case rt.count() == lenSub && rt.seg(idxID) == segActions:
		h.getBundleByName(w, r, rt.seg(idxSub))
	case rt.count() == lenResource:
		h.getBundle(w, r, rt.seg(idxID))
	case rt.count() == lenSub && rt.seg(idxSub) == segVersions:
		h.listBundleVersions(w, r, rt.seg(idxID))
	default:
		notFound(w, r)
	}
}

func (h *Handler) getBundle(w http.ResponseWriter, r *http.Request, secretID string) {
	sel, ok := bundleSelector(w, r)
	if !ok {
		return
	}

	bundle, err := h.extras.GetSecretBundle(secretID, sel)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toBundleResponse(bundle))
}

func (h *Handler) getBundleByName(w http.ResponseWriter, r *http.Request, action string) {
	if action != actionGetByName {
		unknownAction(w, r, action)
		return
	}

	sel, ok := bundleSelector(w, r)
	if !ok {
		return
	}

	bundle, err := h.extras.GetSecretBundleByName(vaultIDOf(r), r.URL.Query().Get("secretName"), sel)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toBundleResponse(bundle))
}

func (h *Handler) listBundleVersions(w http.ResponseWriter, r *http.Request, secretID string) {
	infos, err := h.extras.ListSecretBundleVersions(secretID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	writeList(w, r, infos, toSecretVersionResponse)
}

// bundleSelector reads the three mutually exclusive ways a bundle read names a
// version. The driver rejects more than one; the handler only parses them.
func bundleSelector(w http.ResponseWriter, r *http.Request) (vaultprovider.BundleSelector, bool) {
	query := r.URL.Query()

	sel := vaultprovider.BundleSelector{
		VersionName: query.Get("secretVersionName"),
		Stage:       query.Get("stage"),
	}

	if raw := query.Get("versionNumber"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
				"versionNumber "+raw+" is not a version number")

			return sel, false
		}

		sel.VersionNumber = &n
	}

	return sel, true
}

func toBundleResponse(b *vaultprovider.SecretBundle) secretBundleResponse {
	return secretBundleResponse{
		SecretID:       b.SecretID,
		VersionNumber:  b.VersionNumber,
		VersionName:    b.VersionName,
		Stages:         b.Stages,
		TimeCreated:    b.TimeCreated,
		TimeOfDeletion: b.TimeOfDeletion,
		SecretBundleContent: secretBundleContent{
			ContentType: contentTypeBase64,
			Content:     base64.StdEncoding.EncodeToString(b.Content),
		},
	}
}

// versionIdentifier names a secret version in a work request. OCI gives a
// version no OCID of its own, so the secret and the number identify it.
func versionIdentifier(secretID string, n int64) string {
	return secretID + "/versions/" + strconv.FormatInt(n, 10)
}

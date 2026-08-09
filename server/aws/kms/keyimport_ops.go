package kms

import (
	"context"
	"net/http"
	"time"

	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

type getParametersForImportRequest struct {
	KeyID             string `json:"KeyId"`
	WrappingAlgorithm string `json:"WrappingAlgorithm"`
	WrappingKeySpec   string `json:"WrappingKeySpec"`
}

type getParametersForImportResponse struct {
	KeyID             string   `json:"KeyId"`
	ImportToken       []byte   `json:"ImportToken"`
	PublicKey         []byte   `json:"PublicKey"`
	ParametersValidTo *float64 `json:"ParametersValidTo,omitempty"`
}

type importKeyMaterialRequest struct {
	KeyID                string   `json:"KeyId"`
	ImportToken          []byte   `json:"ImportToken"`
	EncryptedKeyMaterial []byte   `json:"EncryptedKeyMaterial"`
	ValidTo              *float64 `json:"ValidTo"`
	ExpirationModel      string   `json:"ExpirationModel"`
}

type replicateKeyRequest struct {
	KeyID         string `json:"KeyId"`
	ReplicaRegion string `json:"ReplicaRegion"`
	Description   string `json:"Description"`
	Policy        string `json:"Policy"`
	Tags          []tag  `json:"Tags"`
}

type replicateKeyResponse struct {
	ReplicaKeyMetadata keyMetadataJSON `json:"ReplicaKeyMetadata"`
	ReplicaPolicy      string          `json:"ReplicaPolicy"`
	ReplicaTags        []tag           `json:"ReplicaTags"`
}

type updatePrimaryRegionRequest struct {
	KeyID         string `json:"KeyId"`
	PrimaryRegion string `json:"PrimaryRegion"`
}

func (h *Handler) getParametersForImport(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getParametersForImportRequest) (any, error) {
		out, err := h.kms.GetParametersForImport(ctx, kmsdriver.GetParametersForImportInput{
			KeyID: req.KeyID, WrappingAlgorithm: req.WrappingAlgorithm, WrappingKeySpec: req.WrappingKeySpec,
		})
		if err != nil {
			return nil, err
		}

		return getParametersForImportResponse{
			KeyID: out.KeyID, ImportToken: out.ImportToken, PublicKey: out.PublicKey,
			ParametersValidTo: epochOrNil(out.ParametersValidTo),
		}, nil
	})
}

func (h *Handler) importKeyMaterial(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *importKeyMaterialRequest) (any, error) {
		var validTo time.Time
		if req.ValidTo != nil {
			validTo = time.Unix(int64(*req.ValidTo), 0).UTC()
		}

		err := h.kms.ImportKeyMaterial(ctx, kmsdriver.ImportKeyMaterialInput{
			KeyID: req.KeyID, ImportToken: req.ImportToken, EncryptedKeyMaterial: req.EncryptedKeyMaterial,
			ValidTo: validTo, ExpirationModel: req.ExpirationModel,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteImportedKeyMaterial(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *keyIDRequest) (any, error) {
		if err := h.kms.DeleteImportedKeyMaterial(ctx, req.KeyID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) replicateKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *replicateKeyRequest) (any, error) {
		out, err := h.kms.ReplicateKey(ctx, kmsdriver.ReplicateKeyInput{
			KeyID: req.KeyID, ReplicaRegion: req.ReplicaRegion, Description: req.Description,
			Policy: req.Policy, Tags: tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return replicateKeyResponse{
			ReplicaKeyMetadata: metadataJSON(out.ReplicaKeyMetadata),
			ReplicaPolicy:      out.ReplicaPolicy,
			ReplicaTags:        mapToTags(out.ReplicaTags),
		}, nil
	})
}

func (h *Handler) updatePrimaryRegion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updatePrimaryRegionRequest) (any, error) {
		if err := h.kms.UpdatePrimaryRegion(ctx, req.KeyID, req.PrimaryRegion); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

package kms

import (
	"context"
	"errors"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

//nolint:dupl // templated KMS wire handler; the decode/call/respond shape is intrinsic
func (h *Handler) encrypt(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *encryptRequest) (any, error) {
		out, err := h.kms.Encrypt(ctx, kmsdriver.EncryptInput{
			KeyID:               req.KeyID,
			Plaintext:           req.Plaintext,
			EncryptionContext:   req.EncryptionContext,
			EncryptionAlgorithm: req.EncryptionAlgorithm,
		})
		if err != nil {
			return nil, err
		}

		return encryptResponse{
			KeyID: out.KeyID, CiphertextBlob: out.CiphertextBlob, EncryptionAlgorithm: out.EncryptionAlgorithm,
		}, nil
	})
}

//nolint:dupl // templated KMS wire handler; the decode/call/respond shape is intrinsic
func (h *Handler) decrypt(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *decryptRequest) (any, error) {
		out, err := h.kms.Decrypt(ctx, kmsdriver.DecryptInput{
			KeyID:               req.KeyID,
			CiphertextBlob:      req.CiphertextBlob,
			EncryptionContext:   req.EncryptionContext,
			EncryptionAlgorithm: req.EncryptionAlgorithm,
		})
		if err != nil {
			return nil, err
		}

		return decryptResponse{
			KeyID: out.KeyID, Plaintext: out.Plaintext, EncryptionAlgorithm: out.EncryptionAlgorithm,
		}, nil
	})
}

func (h *Handler) reEncrypt(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *reEncryptRequest) (any, error) {
		out, err := h.kms.ReEncrypt(ctx, kmsdriver.ReEncryptInput{
			CiphertextBlob:                 req.CiphertextBlob,
			SourceKeyID:                    req.SourceKeyID,
			SourceEncryptionContext:        req.SourceEncryptionContext,
			DestinationKeyID:               req.DestinationKeyID,
			DestinationEncryptionContext:   req.DestinationEncryptionContext,
			SourceEncryptionAlgorithm:      req.SourceEncryptionAlgorithm,
			DestinationEncryptionAlgorithm: req.DestinationEncryptionAlgorithm,
		})
		if err != nil {
			return nil, err
		}

		return reEncryptResponse{
			CiphertextBlob:                 out.CiphertextBlob,
			SourceKeyID:                    out.SourceKeyID,
			KeyID:                          out.KeyID,
			SourceEncryptionAlgorithm:      out.SourceEncryptionAlgorithm,
			DestinationEncryptionAlgorithm: out.DestinationEncryptionAlgorithm,
		}, nil
	})
}

//nolint:dupl // templated KMS wire handler; the decode/call/respond shape is intrinsic
func (h *Handler) generateDataKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *generateDataKeyRequest) (any, error) {
		out, err := h.kms.GenerateDataKey(ctx, kmsdriver.GenerateDataKeyInput{
			KeyID: req.KeyID, KeySpec: req.KeySpec, NumberOfBytes: req.NumberOfBytes,
			EncryptionContext: req.EncryptionContext,
		})
		if err != nil {
			return nil, err
		}

		return generateDataKeyResponse{
			KeyID: out.KeyID, Plaintext: out.Plaintext, CiphertextBlob: out.CiphertextBlob,
		}, nil
	})
}

func (h *Handler) generateDataKeyWithoutPlaintext(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *generateDataKeyRequest) (any, error) {
		out, err := h.kms.GenerateDataKeyWithoutPlaintext(ctx, kmsdriver.GenerateDataKeyInput{
			KeyID: req.KeyID, KeySpec: req.KeySpec, NumberOfBytes: req.NumberOfBytes,
			EncryptionContext: req.EncryptionContext,
		})
		if err != nil {
			return nil, err
		}

		return generateDataKeyResponse{KeyID: out.KeyID, CiphertextBlob: out.CiphertextBlob}, nil
	})
}

func (h *Handler) generateDataKeyPair(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *generateDataKeyPairRequest) (any, error) {
		out, err := h.kms.GenerateDataKeyPair(ctx, kmsdriver.GenerateDataKeyPairInput{
			KeyID: req.KeyID, KeyPairSpec: req.KeyPairSpec, EncryptionContext: req.EncryptionContext,
		})
		if err != nil {
			return nil, err
		}

		return generateDataKeyPairResponse{
			KeyID:                    out.KeyID,
			KeyPairSpec:              out.KeyPairSpec,
			PublicKey:                out.PublicKey,
			PrivateKeyPlaintext:      out.PrivateKeyPlaintext,
			PrivateKeyCiphertextBlob: out.PrivateKeyCiphertextBlob,
		}, nil
	})
}

func (h *Handler) generateDataKeyPairWithoutPlaintext(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *generateDataKeyPairRequest) (any, error) {
		out, err := h.kms.GenerateDataKeyPairWithoutPlaintext(ctx, kmsdriver.GenerateDataKeyPairInput{
			KeyID: req.KeyID, KeyPairSpec: req.KeyPairSpec, EncryptionContext: req.EncryptionContext,
		})
		if err != nil {
			return nil, err
		}

		return generateDataKeyPairResponse{
			KeyID:                    out.KeyID,
			KeyPairSpec:              out.KeyPairSpec,
			PublicKey:                out.PublicKey,
			PrivateKeyCiphertextBlob: out.PrivateKeyCiphertextBlob,
		}, nil
	})
}

func (h *Handler) generateRandom(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *generateRandomRequest) (any, error) {
		out, err := h.kms.GenerateRandom(ctx, req.NumberOfBytes)
		if err != nil {
			return nil, err
		}

		return generateRandomResponse{Plaintext: out}, nil
	})
}

//nolint:dupl // templated KMS wire handler; the decode/call/respond shape is intrinsic
func (h *Handler) sign(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *signRequest) (any, error) {
		out, err := h.kms.Sign(ctx, kmsdriver.SignInput{
			KeyID: req.KeyID, Message: req.Message, MessageType: req.MessageType,
			SigningAlgorithm: req.SigningAlgorithm,
		})
		if err != nil {
			return nil, err
		}

		return signResponse{
			KeyID: out.KeyID, Signature: out.Signature, SigningAlgorithm: out.SigningAlgorithm,
		}, nil
	})
}

func (h *Handler) generateMac(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *generateMacRequest) (any, error) {
		out, err := h.kms.GenerateMac(ctx, kmsdriver.GenerateMacInput{
			KeyID: req.KeyID, Message: req.Message, MacAlgorithm: req.MacAlgorithm,
		})
		if err != nil {
			return nil, err
		}

		return generateMacResponse{KeyID: out.KeyID, Mac: out.Mac, MacAlgorithm: out.MacAlgorithm}, nil
	})
}

// verify and verifyMac don't use dispatch: a failed verification maps to a
// distinct KMS exception rather than the generic error path.
func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := h.kms.Verify(r.Context(), kmsdriver.VerifyInput{
		KeyID: req.KeyID, Message: req.Message, MessageType: req.MessageType,
		Signature: req.Signature, SigningAlgorithm: req.SigningAlgorithm,
	})
	if err != nil {
		if errors.Is(err, kmsdriver.ErrSignatureInvalid) {
			wire.WriteJSONError(w, http.StatusBadRequest, "KMSInvalidSignatureException", err.Error())
			return
		}

		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, verifyResponse{
		KeyID: out.KeyID, SignatureValid: out.SignatureValid, SigningAlgorithm: out.SigningAlgorithm,
	})
}

func (h *Handler) verifyMac(w http.ResponseWriter, r *http.Request) {
	var req verifyMacRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := h.kms.VerifyMac(r.Context(), kmsdriver.VerifyMacInput{
		KeyID: req.KeyID, Message: req.Message, Mac: req.Mac, MacAlgorithm: req.MacAlgorithm,
	})
	if err != nil {
		if errors.Is(err, kmsdriver.ErrMacInvalid) {
			wire.WriteJSONError(w, http.StatusBadRequest, "KMSInvalidMacException", err.Error())
			return
		}

		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, verifyMacResponse{KeyID: out.KeyID, MacValid: out.MacValid, MacAlgorithm: out.MacAlgorithm})
}

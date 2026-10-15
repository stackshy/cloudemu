package kms

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	kmsprov "github.com/stackshy/cloudemu/v2/providers/gcp/kms"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	verbEncrypt             = "encrypt"
	verbDecrypt             = "decrypt"
	verbAsymmetricSign      = "asymmetricSign"
	verbAsymmetricDecrypt   = "asymmetricDecrypt"
	verbMacSign             = "macSign"
	verbMacVerify           = "macVerify"
	verbPublicKey           = "publicKey"
	verbGenerateRandomBytes = "generateRandomBytes"

	// maxPayloadBytes is Cloud KMS's 64 KiB cap on plaintext, AAD and data.
	maxPayloadBytes = 64 * 1024
)

func tooLarge(field string, b []byte) error {
	if len(b) > maxPayloadBytes {
		return cerrors.Newf(cerrors.InvalidArgument, "%s exceeds the %d-byte limit", field, maxPayloadBytes)
	}

	return nil
}

func (h *Handler) encrypt(w http.ResponseWriter, r *http.Request, rt *route) {
	var req encryptRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	resp, err := h.doEncrypt(rt, &req)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) doEncrypt(rt *route, req *encryptRequest) (*encryptResponse, error) {
	if err := tooLarge("plaintext", req.Plaintext); err != nil {
		return nil, err
	}

	if err := tooLarge("additionalAuthenticatedData", req.AdditionalAuthenticatedData); err != nil {
		return nil, err
	}

	ptOK, err := checkCRC("plaintext_crc32c", "plaintext", req.Plaintext, req.PlaintextCrc32c)
	if err != nil {
		return nil, err
	}

	aadOK, err := checkCRC("additional_authenticated_data_crc32c", "additional_authenticated_data",
		req.AdditionalAuthenticatedData, req.AdditionalAuthenticatedDataCrc32c)
	if err != nil {
		return nil, err
	}

	res, err := h.kms.Encrypt(rt.ref(), req.Plaintext, req.AdditionalAuthenticatedData)
	if err != nil {
		return nil, err
	}

	return &encryptResponse{
		Name: res.Name, Ciphertext: res.Out, CiphertextCrc32c: crcOf(res.Out),
		VerifiedPlaintextCrc32c: ptOK, VerifiedAdditionalAuthenticatedDataCrc32c: aadOK,
		ProtectionLevel: res.ProtectionLevel,
	}, nil
}

func (h *Handler) decrypt(w http.ResponseWriter, r *http.Request, rt *route) {
	var req decryptRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	_, err := checkCRC("ciphertext_crc32c", "ciphertext", req.Ciphertext, req.CiphertextCrc32c)
	if err == nil {
		_, err = checkCRC("additional_authenticated_data_crc32c", "additional_authenticated_data",
			req.AdditionalAuthenticatedData, req.AdditionalAuthenticatedDataCrc32c)
	}

	if err != nil {
		writeKMSErr(w, err)
		return
	}

	res, err := h.kms.Decrypt(rt.ref(), req.Ciphertext, req.AdditionalAuthenticatedData)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, decryptResponse{
		Plaintext: res.Out, PlaintextCrc32c: crcOf(res.Out),
		UsedPrimary: res.UsedPrimary, ProtectionLevel: res.ProtectionLevel,
	})
}

// suppliedDigest returns whichever digest field the request set, the bytes
// digestCrc32c covers.
func suppliedDigest(d *digestJSON) []byte {
	if d == nil {
		return nil
	}

	for _, b := range [][]byte{d.Sha256, d.Sha384, d.Sha512} {
		if len(b) > 0 {
			return b
		}
	}

	return nil
}

func (h *Handler) asymmetricSign(w http.ResponseWriter, r *http.Request, rt *route) {
	var req asymmetricSignRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	var (
		resp asymmetricSignResponse
		err  error
	)

	resp.VerifiedDigestCrc32c, err = checkCRC("digest_crc32c", "digest", suppliedDigest(req.Digest), req.DigestCrc32c)
	if err == nil {
		resp.VerifiedDataCrc32c, err = checkCRC("data_crc32c", "data", req.Data, req.DataCrc32c)
	}

	var digest *kmsprov.Digest
	if req.Digest != nil {
		digest = &kmsprov.Digest{Sha256: req.Digest.Sha256, Sha384: req.Digest.Sha384, Sha512: req.Digest.Sha512}
	}

	var res kmsprov.Result
	if err == nil {
		res, err = h.kms.AsymmetricSign(rt.ref(), digest, req.Data)
	}

	if err != nil {
		writeKMSErr(w, err)
		return
	}

	resp.Signature, resp.SignatureCrc32c = res.Out, crcOf(res.Out)
	resp.Name, resp.ProtectionLevel = res.Name, res.ProtectionLevel

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) asymmetricDecrypt(w http.ResponseWriter, r *http.Request, rt *route) {
	var req asymmetricDecryptRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	verified, err := checkCRC("ciphertext_crc32c", "ciphertext", req.Ciphertext, req.CiphertextCrc32c)

	var res kmsprov.Result
	if err == nil {
		res, err = h.kms.AsymmetricDecrypt(rt.ref(), req.Ciphertext)
	}

	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, asymmetricDecryptResponse{
		Plaintext: res.Out, PlaintextCrc32c: crcOf(res.Out),
		VerifiedCiphertextCrc32c: verified, ProtectionLevel: res.ProtectionLevel,
	})
}

// getPublicKey serves GET .../cryptoKeyVersions/{v}/publicKey for either
// asymmetric purpose.
func (h *Handler) getPublicKey(w http.ResponseWriter, rt *route) {
	pk, err := h.kms.GetPublicKey(rt.ref())
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, publicKeyResponse{
		Pem: pk.Pem, Algorithm: pk.Algorithm, PemCrc32c: crcOf([]byte(pk.Pem)),
		Name: pk.Name, ProtectionLevel: pk.ProtectionLevel, PublicKeyFormat: "PEM",
	})
}

// macOp serves macSign and macVerify with HMAC under the version's key.
func (h *Handler) macOp(w http.ResponseWriter, r *http.Request, rt *route) {
	var req macRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	dataOK, err := checkCRC("data_crc32c", "data", req.Data, req.DataCrc32c)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	if rt.verb == verbMacSign {
		res, signErr := h.kms.MacSign(rt.ref(), req.Data)
		if signErr != nil {
			writeKMSErr(w, signErr)
			return
		}

		gcprest.WriteJSON(w, http.StatusOK, macSignResponse{
			Name: res.Name, Mac: res.Out, MacCrc32c: crcOf(res.Out),
			VerifiedDataCrc32c: dataOK, ProtectionLevel: res.ProtectionLevel,
		})

		return
	}

	macOK, err := checkCRC("mac_crc32c", "mac", req.Mac, req.MacCrc32c)

	var (
		res     kmsprov.Result
		success bool
	)

	if err == nil {
		res, success, err = h.kms.MacVerify(rt.ref(), req.Data, req.Mac)
	}

	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, macVerifyResponse{
		Name: res.Name, Success: success, VerifiedDataCrc32c: dataOK,
		VerifiedMacCrc32c: macOK, VerifiedSuccessIntegrity: true, ProtectionLevel: res.ProtectionLevel,
	})
}

// generateRandomBytes serves POST /v1/projects/{p}/locations/{l}:generateRandomBytes.
func (h *Handler) generateRandomBytes(w http.ResponseWriter, r *http.Request) {
	var req generateRandomBytesRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	b, err := h.kms.GenerateRandomBytes(req.LengthBytes)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, generateRandomBytesResponse{Data: b, DataCrc32c: crcOf(b)})
}

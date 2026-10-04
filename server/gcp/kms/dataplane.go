package kms

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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

	purposeAsymmetricSign    = "ASYMMETRIC_SIGN"
	purposeAsymmetricDecrypt = "ASYMMETRIC_DECRYPT"
	purposeMAC               = "MAC"

	// maxPayloadBytes is Cloud KMS's 64 KiB cap on plaintext, AAD and data.
	maxPayloadBytes = 64 * 1024
	// maxRandomBytes is the generateRandomBytes lengthBytes upper bound.
	maxRandomBytes = 1024
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

	res, err := h.store.encrypt(rt, req.Plaintext, req.AdditionalAuthenticatedData)
	if err != nil {
		return nil, err
	}

	return &encryptResponse{
		Name: res.name, Ciphertext: res.out, CiphertextCrc32c: crcOf(res.out),
		VerifiedPlaintextCrc32c: ptOK, VerifiedAdditionalAuthenticatedDataCrc32c: aadOK,
		ProtectionLevel: res.protectionLevel,
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

	res, err := h.store.decrypt(rt, req.Ciphertext, req.AdditionalAuthenticatedData)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, decryptResponse{
		Plaintext: res.out, PlaintextCrc32c: crcOf(res.out),
		UsedPrimary: res.usedPrimary, ProtectionLevel: res.protectionLevel,
	})
}

// signInput resolves the bytes the algorithm signs: the raw data for Ed25519
// and RSA_SIGN_RAW_PKCS1_*, otherwise a digest (supplied, or computed from
// data) of the algorithm's hash.
func signInput(alg string, req *asymmetricSignRequest) (digest []byte, err error) {
	if alg == algEd25519 || strings.HasPrefix(alg, "RSA_SIGN_RAW_PKCS1") {
		if len(req.Data) == 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "data is required for algorithm %s", alg)
		}

		return nil, nil
	}

	hash := hashFor(alg)

	if req.Digest == nil {
		if len(req.Data) == 0 {
			return nil, cerrors.New(cerrors.InvalidArgument, "one of digest or data is required")
		}

		hh := hash.New()
		hh.Write(req.Data)

		return hh.Sum(nil), nil
	}

	digests := map[crypto.Hash][]byte{
		crypto.SHA256: req.Digest.Sha256, crypto.SHA384: req.Digest.Sha384, crypto.SHA512: req.Digest.Sha512,
	}

	d := digests[hash]
	if len(d) != hash.Size() {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"digest must be a %d-byte %s digest for algorithm %s", hash.Size(), hash, alg)
	}

	return d, nil
}

func (h *Handler) asymmetricSign(w http.ResponseWriter, r *http.Request, rt *route) {
	var req asymmetricSignRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	var resp asymmetricSignResponse

	err := h.store.withVersion(rt, func(name string, v *versionModel) error {
		digest, err := signInput(v.algorithm, &req)
		if err != nil {
			return err
		}

		if resp.VerifiedDigestCrc32c, err = checkCRC("digest_crc32c", "digest", digest, req.DigestCrc32c); err != nil {
			return err
		}

		if resp.VerifiedDataCrc32c, err = checkCRC("data_crc32c", "data", req.Data, req.DataCrc32c); err != nil {
			return err
		}

		sig, err := v.sign(digest, req.Data)
		if err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "sign: %v", err)
		}

		resp.Signature, resp.SignatureCrc32c = sig, crcOf(sig)
		resp.Name, resp.ProtectionLevel = name, v.protectionLevel

		return nil
	}, purposeAsymmetricSign)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) asymmetricDecrypt(w http.ResponseWriter, r *http.Request, rt *route) {
	var req asymmetricDecryptRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	var resp asymmetricDecryptResponse

	err := h.store.withVersion(rt, func(_ string, v *versionModel) error {
		var err error
		if resp.VerifiedCiphertextCrc32c, err = checkCRC("ciphertext_crc32c", "ciphertext",
			req.Ciphertext, req.CiphertextCrc32c); err != nil {
			return err
		}

		pt, err := v.decryptOAEP(req.Ciphertext)
		if err != nil {
			return err
		}

		resp.Plaintext, resp.PlaintextCrc32c, resp.ProtectionLevel = pt, crcOf(pt), v.protectionLevel

		return nil
	}, purposeAsymmetricDecrypt)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

// getPublicKey serves GET .../cryptoKeyVersions/{v}/publicKey for either
// asymmetric purpose.
func (h *Handler) getPublicKey(w http.ResponseWriter, rt *route) {
	var resp publicKeyResponse

	fn := func(name string, v *versionModel) error {
		p, err := v.publicKeyPEM()
		if err != nil {
			return err
		}

		resp = publicKeyResponse{
			Pem: p, Algorithm: v.algorithm, PemCrc32c: crcOf([]byte(p)),
			Name: name, ProtectionLevel: v.protectionLevel, PublicKeyFormat: "PEM",
		}

		return nil
	}

	if err := h.store.withVersion(rt, fn, purposeAsymmetricSign, purposeAsymmetricDecrypt); err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

// macOp serves macSign and macVerify with HMAC under the version's key.
func (h *Handler) macOp(w http.ResponseWriter, r *http.Request, rt *route) {
	var req macRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	var resp any

	err := h.store.withVersion(rt, func(name string, v *versionModel) error {
		dataOK, err := checkCRC("data_crc32c", "data", req.Data, req.DataCrc32c)
		if err != nil {
			return err
		}

		mac := hmac.New(hashFor(v.algorithm).New, v.secret)
		mac.Write(req.Data)
		sum := mac.Sum(nil)

		if rt.verb == verbMacSign {
			resp = macSignResponse{
				Name: name, Mac: sum, MacCrc32c: crcOf(sum),
				VerifiedDataCrc32c: dataOK, ProtectionLevel: v.protectionLevel,
			}

			return nil
		}

		macOK, err := checkCRC("mac_crc32c", "mac", req.Mac, req.MacCrc32c)
		if err != nil {
			return err
		}

		resp = macVerifyResponse{
			Name: name, Success: hmac.Equal(sum, req.Mac), VerifiedDataCrc32c: dataOK,
			VerifiedMacCrc32c: macOK, VerifiedSuccessIntegrity: true, ProtectionLevel: v.protectionLevel,
		}

		return nil
	}, purposeMAC)
	if err != nil {
		writeKMSErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, resp)
}

// generateRandomBytes serves POST /v1/projects/{p}/locations/{l}:generateRandomBytes.
func (*Handler) generateRandomBytes(w http.ResponseWriter, r *http.Request) {
	var req generateRandomBytesRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.LengthBytes < 8 || req.LengthBytes > maxRandomBytes {
		invalidArg(w, "lengthBytes must be between 8 and 1024")
		return
	}

	b := make([]byte, req.LengthBytes)
	if _, err := rand.Read(b); err != nil {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.Internal, "random: %v", err))
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, generateRandomBytesResponse{Data: b, DataCrc32c: crcOf(b)})
}

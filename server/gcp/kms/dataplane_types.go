package kms

import (
	"hash/crc32"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// jsonNull is the JSON literal an omitted optional field may carry.
const jsonNull = "null"

// crcValue is a google.protobuf.Int64Value CRC32C checksum. proto3 JSON sends
// it as a quoted string, but a bare number is accepted too.
type crcValue struct {
	v   uint32
	set bool
}

// UnmarshalJSON accepts "123", 123 or null.
func (c *crcValue) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == jsonNull {
		return nil
	}

	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return err
	}

	c.v, c.set = uint32(n), true

	return nil
}

//nolint:gochecknoglobals // immutable CRC32C table shared by every checksum
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// crcOf returns the CRC32C of b in its proto3 JSON (decimal string) form.
func crcOf(b []byte) string {
	return strconv.FormatUint(uint64(crc32.Checksum(b, castagnoli)), 10)
}

// checkCRC verifies a client-supplied checksum. verified is true when the
// checksum was supplied and matched; a mismatch is INVALID_ARGUMENT, as in
// real Cloud KMS.
func checkCRC(crcField, dataField string, data []byte, c crcValue) (verified bool, err error) {
	if !c.set {
		return false, nil
	}

	if crc32.Checksum(data, castagnoli) != c.v {
		return false, cerrors.Newf(cerrors.InvalidArgument,
			"The checksum in field %s did not match the data in field %s.", crcField, dataField)
	}

	return true, nil
}

type encryptRequest struct {
	Plaintext                         []byte   `json:"plaintext"`
	AdditionalAuthenticatedData       []byte   `json:"additionalAuthenticatedData"`
	PlaintextCrc32c                   crcValue `json:"plaintextCrc32c"`
	AdditionalAuthenticatedDataCrc32c crcValue `json:"additionalAuthenticatedDataCrc32c"`
}

type encryptResponse struct {
	Name                                      string `json:"name"`
	Ciphertext                                []byte `json:"ciphertext"`
	CiphertextCrc32c                          string `json:"ciphertextCrc32c"`
	VerifiedPlaintextCrc32c                   bool   `json:"verifiedPlaintextCrc32c,omitempty"`
	VerifiedAdditionalAuthenticatedDataCrc32c bool   `json:"verifiedAdditionalAuthenticatedDataCrc32c,omitempty"`
	ProtectionLevel                           string `json:"protectionLevel,omitempty"`
}

type decryptRequest struct {
	Ciphertext                        []byte   `json:"ciphertext"`
	AdditionalAuthenticatedData       []byte   `json:"additionalAuthenticatedData"`
	CiphertextCrc32c                  crcValue `json:"ciphertextCrc32c"`
	AdditionalAuthenticatedDataCrc32c crcValue `json:"additionalAuthenticatedDataCrc32c"`
}

type decryptResponse struct {
	Plaintext       []byte `json:"plaintext"`
	PlaintextCrc32c string `json:"plaintextCrc32c"`
	UsedPrimary     bool   `json:"usedPrimary,omitempty"`
	ProtectionLevel string `json:"protectionLevel,omitempty"`
}

type digestJSON struct {
	Sha256 []byte `json:"sha256"`
	Sha384 []byte `json:"sha384"`
	Sha512 []byte `json:"sha512"`
}

type asymmetricSignRequest struct {
	Digest       *digestJSON `json:"digest"`
	DigestCrc32c crcValue    `json:"digestCrc32c"`
	Data         []byte      `json:"data"`
	DataCrc32c   crcValue    `json:"dataCrc32c"`
}

type asymmetricSignResponse struct {
	Signature            []byte `json:"signature"`
	SignatureCrc32c      string `json:"signatureCrc32c"`
	VerifiedDigestCrc32c bool   `json:"verifiedDigestCrc32c,omitempty"`
	VerifiedDataCrc32c   bool   `json:"verifiedDataCrc32c,omitempty"`
	Name                 string `json:"name"`
	ProtectionLevel      string `json:"protectionLevel,omitempty"`
}

type asymmetricDecryptRequest struct {
	Ciphertext       []byte   `json:"ciphertext"`
	CiphertextCrc32c crcValue `json:"ciphertextCrc32c"`
}

type asymmetricDecryptResponse struct {
	Plaintext                []byte `json:"plaintext"`
	PlaintextCrc32c          string `json:"plaintextCrc32c"`
	VerifiedCiphertextCrc32c bool   `json:"verifiedCiphertextCrc32c,omitempty"`
	ProtectionLevel          string `json:"protectionLevel,omitempty"`
}

type publicKeyResponse struct {
	Pem             string `json:"pem"`
	Algorithm       string `json:"algorithm"`
	PemCrc32c       string `json:"pemCrc32c"`
	Name            string `json:"name"`
	ProtectionLevel string `json:"protectionLevel,omitempty"`
	PublicKeyFormat string `json:"publicKeyFormat,omitempty"`
}

type macRequest struct {
	Data       []byte   `json:"data"`
	DataCrc32c crcValue `json:"dataCrc32c"`
	Mac        []byte   `json:"mac"`
	MacCrc32c  crcValue `json:"macCrc32c"`
}

type macSignResponse struct {
	Name               string `json:"name"`
	Mac                []byte `json:"mac"`
	MacCrc32c          string `json:"macCrc32c"`
	VerifiedDataCrc32c bool   `json:"verifiedDataCrc32c,omitempty"`
	ProtectionLevel    string `json:"protectionLevel,omitempty"`
}

type macVerifyResponse struct {
	Name                     string `json:"name"`
	Success                  bool   `json:"success"`
	VerifiedDataCrc32c       bool   `json:"verifiedDataCrc32c,omitempty"`
	VerifiedMacCrc32c        bool   `json:"verifiedMacCrc32c,omitempty"`
	VerifiedSuccessIntegrity bool   `json:"verifiedSuccessIntegrity,omitempty"`
	ProtectionLevel          string `json:"protectionLevel,omitempty"`
}

type generateRandomBytesRequest struct {
	LengthBytes int `json:"lengthBytes"`
}

type generateRandomBytesResponse struct {
	Data       []byte `json:"data"`
	DataCrc32c string `json:"dataCrc32c"`
}

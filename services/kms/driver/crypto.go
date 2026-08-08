package driver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Sentinel errors so the wire layer can map a failed cryptographic
// verification to the precise KMS exception (KMSInvalidSignatureException /
// KMSInvalidMacException) rather than a generic validation error.
var (
	ErrSignatureInvalid = errors.New(errors.InvalidArgument, "signature verification failed")
	ErrMacInvalid       = errors.New(errors.InvalidArgument, "MAC verification failed")
)

// Encryption algorithms.
const (
	EncSymmetricDefault = "SYMMETRIC_DEFAULT"
	EncRSAOAEPSHA1      = "RSAES_OAEP_SHA_1"
	EncRSAOAEPSHA256    = "RSAES_OAEP_SHA_256"
)

// Signing algorithms.
const (
	SignRSASSAPSSSHA256   = "RSASSA_PSS_SHA_256"
	SignRSASSAPSSSHA384   = "RSASSA_PSS_SHA_384"
	SignRSASSAPSSSHA512   = "RSASSA_PSS_SHA_512"
	SignRSASSAPKCS1SHA256 = "RSASSA_PKCS1_V1_5_SHA_256"
	SignRSASSAPKCS1SHA384 = "RSASSA_PKCS1_V1_5_SHA_384"
	SignRSASSAPKCS1SHA512 = "RSASSA_PKCS1_V1_5_SHA_512"
	SignECDSASHA256       = "ECDSA_SHA_256"
	SignECDSASHA384       = "ECDSA_SHA_384"
	SignECDSASHA512       = "ECDSA_SHA_512"
)

// MAC algorithms.
const (
	MacHMACSHA256 = "HMAC_SHA_256"
	MacHMACSHA384 = "HMAC_SHA_384"
	MacHMACSHA512 = "HMAC_SHA_512"
)

// Message types for Sign/Verify.
const (
	MessageTypeRaw    = "RAW"
	MessageTypeDigest = "DIGEST"
)

// Data key specs.
const (
	DataKeyAES256 = "AES_256"
	DataKeyAES128 = "AES_128"
)

// Data key pair specs.
const (
	DataKeyPairRSA2048     = "RSA_2048"
	DataKeyPairRSA3072     = "RSA_3072"
	DataKeyPairRSA4096     = "RSA_4096"
	DataKeyPairECCNISTP256 = "ECC_NIST_P256"
	DataKeyPairECCNISTP384 = "ECC_NIST_P384"
	DataKeyPairECCNISTP521 = "ECC_NIST_P521"
)

// EncryptInput describes an Encrypt request.
type EncryptInput struct {
	KeyID               string
	Plaintext           []byte
	EncryptionContext   map[string]string
	EncryptionAlgorithm string
}

// EncryptOutput is the result of Encrypt.
type EncryptOutput struct {
	KeyID               string
	CiphertextBlob      []byte
	EncryptionAlgorithm string
}

// DecryptInput describes a Decrypt request. KeyID is optional for symmetric
// ciphertext (the blob is self-describing) and required for asymmetric.
type DecryptInput struct {
	KeyID               string
	CiphertextBlob      []byte
	EncryptionContext   map[string]string
	EncryptionAlgorithm string
}

// DecryptOutput is the result of Decrypt.
type DecryptOutput struct {
	KeyID               string
	Plaintext           []byte
	EncryptionAlgorithm string
}

// ReEncryptInput describes a ReEncrypt request.
type ReEncryptInput struct {
	CiphertextBlob                 []byte
	SourceKeyID                    string
	SourceEncryptionContext        map[string]string
	DestinationKeyID               string
	DestinationEncryptionContext   map[string]string
	SourceEncryptionAlgorithm      string
	DestinationEncryptionAlgorithm string
}

// ReEncryptOutput is the result of ReEncrypt.
type ReEncryptOutput struct {
	CiphertextBlob                 []byte
	SourceKeyID                    string
	KeyID                          string
	SourceEncryptionAlgorithm      string
	DestinationEncryptionAlgorithm string
}

// GenerateDataKeyInput describes a GenerateDataKey request. Exactly one of
// KeySpec / NumberOfBytes is used; KeySpec wins when both are set.
type GenerateDataKeyInput struct {
	KeyID             string
	KeySpec           string
	NumberOfBytes     int32
	EncryptionContext map[string]string
}

// GenerateDataKeyOutput carries the plaintext data key and its ciphertext.
type GenerateDataKeyOutput struct {
	KeyID          string
	Plaintext      []byte
	CiphertextBlob []byte
}

// GenerateDataKeyPairInput describes a GenerateDataKeyPair request.
type GenerateDataKeyPairInput struct {
	KeyID             string
	KeyPairSpec       string
	EncryptionContext map[string]string
}

// GenerateDataKeyPairOutput carries the DER public key, plaintext private key
// (PKCS#8 DER), and the private key encrypted under the KMS key.
type GenerateDataKeyPairOutput struct {
	KeyID                    string
	KeyPairSpec              string
	PublicKey                []byte
	PrivateKeyPlaintext      []byte
	PrivateKeyCiphertextBlob []byte
}

// SignInput describes a Sign request.
type SignInput struct {
	KeyID            string
	Message          []byte
	MessageType      string
	SigningAlgorithm string
}

// SignOutput carries the signature.
type SignOutput struct {
	KeyID            string
	Signature        []byte
	SigningAlgorithm string
}

// VerifyInput describes a Verify request.
type VerifyInput struct {
	KeyID            string
	Message          []byte
	MessageType      string
	Signature        []byte
	SigningAlgorithm string
}

// VerifyOutput reports whether the signature is valid.
type VerifyOutput struct {
	KeyID            string
	SignatureValid   bool
	SigningAlgorithm string
}

// GenerateMacInput describes a GenerateMac request.
type GenerateMacInput struct {
	KeyID        string
	Message      []byte
	MacAlgorithm string
}

// GenerateMacOutput carries the MAC.
type GenerateMacOutput struct {
	KeyID        string
	Mac          []byte
	MacAlgorithm string
}

// VerifyMacInput describes a VerifyMac request.
type VerifyMacInput struct {
	KeyID        string
	Message      []byte
	Mac          []byte
	MacAlgorithm string
}

// VerifyMacOutput reports whether the MAC is valid.
type VerifyMacOutput struct {
	KeyID        string
	MacValid     bool
	MacAlgorithm string
}

// Crypto is the cryptographic surface of KMS. It is embedded in the KMS
// interface; kept separate here to group the operations.
type Crypto interface {
	Encrypt(ctx context.Context, in EncryptInput) (*EncryptOutput, error)
	Decrypt(ctx context.Context, in DecryptInput) (*DecryptOutput, error)
	ReEncrypt(ctx context.Context, in ReEncryptInput) (*ReEncryptOutput, error)
	GenerateDataKey(ctx context.Context, in GenerateDataKeyInput) (*GenerateDataKeyOutput, error)
	GenerateDataKeyWithoutPlaintext(ctx context.Context, in GenerateDataKeyInput) (*GenerateDataKeyOutput, error)
	GenerateDataKeyPair(ctx context.Context, in GenerateDataKeyPairInput) (*GenerateDataKeyPairOutput, error)
	GenerateDataKeyPairWithoutPlaintext(ctx context.Context, in GenerateDataKeyPairInput) (*GenerateDataKeyPairOutput, error)
	GenerateRandom(ctx context.Context, numberOfBytes int32) ([]byte, error)
	Sign(ctx context.Context, in SignInput) (*SignOutput, error)
	Verify(ctx context.Context, in VerifyInput) (*VerifyOutput, error)
	GenerateMac(ctx context.Context, in GenerateMacInput) (*GenerateMacOutput, error)
	VerifyMac(ctx context.Context, in VerifyMacInput) (*VerifyMacOutput, error)
}

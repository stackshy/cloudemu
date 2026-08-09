package kms

// Cryptographic request/response wire shapes. Blob fields are []byte, which
// encoding/json base64-encodes on the wire — exactly the AWS JSON blob shape.

type encryptRequest struct {
	KeyID               string            `json:"KeyId"`
	Plaintext           []byte            `json:"Plaintext"`
	EncryptionContext   map[string]string `json:"EncryptionContext"`
	EncryptionAlgorithm string            `json:"EncryptionAlgorithm"`
}

type encryptResponse struct {
	KeyID               string `json:"KeyId"`
	CiphertextBlob      []byte `json:"CiphertextBlob"`
	EncryptionAlgorithm string `json:"EncryptionAlgorithm"`
}

type decryptRequest struct {
	KeyID               string            `json:"KeyId"`
	CiphertextBlob      []byte            `json:"CiphertextBlob"`
	EncryptionContext   map[string]string `json:"EncryptionContext"`
	EncryptionAlgorithm string            `json:"EncryptionAlgorithm"`
}

type decryptResponse struct {
	KeyID               string `json:"KeyId"`
	Plaintext           []byte `json:"Plaintext"`
	EncryptionAlgorithm string `json:"EncryptionAlgorithm"`
}

type reEncryptRequest struct {
	CiphertextBlob                 []byte            `json:"CiphertextBlob"`
	SourceKeyID                    string            `json:"SourceKeyId"`
	SourceEncryptionContext        map[string]string `json:"SourceEncryptionContext"`
	DestinationKeyID               string            `json:"DestinationKeyId"`
	DestinationEncryptionContext   map[string]string `json:"DestinationEncryptionContext"`
	SourceEncryptionAlgorithm      string            `json:"SourceEncryptionAlgorithm"`
	DestinationEncryptionAlgorithm string            `json:"DestinationEncryptionAlgorithm"`
}

type reEncryptResponse struct {
	CiphertextBlob                 []byte `json:"CiphertextBlob"`
	SourceKeyID                    string `json:"SourceKeyId"`
	KeyID                          string `json:"KeyId"`
	SourceEncryptionAlgorithm      string `json:"SourceEncryptionAlgorithm"`
	DestinationEncryptionAlgorithm string `json:"DestinationEncryptionAlgorithm"`
}

type generateDataKeyRequest struct {
	KeyID             string            `json:"KeyId"`
	KeySpec           string            `json:"KeySpec"`
	NumberOfBytes     int32             `json:"NumberOfBytes"`
	EncryptionContext map[string]string `json:"EncryptionContext"`
}

type generateDataKeyResponse struct {
	KeyID          string `json:"KeyId"`
	Plaintext      []byte `json:"Plaintext,omitempty"`
	CiphertextBlob []byte `json:"CiphertextBlob"`
}

type generateDataKeyPairRequest struct {
	KeyID             string            `json:"KeyId"`
	KeyPairSpec       string            `json:"KeyPairSpec"`
	EncryptionContext map[string]string `json:"EncryptionContext"`
}

type generateDataKeyPairResponse struct {
	KeyID                    string `json:"KeyId"`
	KeyPairSpec              string `json:"KeyPairSpec"`
	PublicKey                []byte `json:"PublicKey"`
	PrivateKeyPlaintext      []byte `json:"PrivateKeyPlaintext,omitempty"`
	PrivateKeyCiphertextBlob []byte `json:"PrivateKeyCiphertextBlob"`
}

type generateRandomRequest struct {
	NumberOfBytes int32 `json:"NumberOfBytes"`
}

type generateRandomResponse struct {
	Plaintext []byte `json:"Plaintext"`
}

type signRequest struct {
	KeyID            string `json:"KeyId"`
	Message          []byte `json:"Message"`
	MessageType      string `json:"MessageType"`
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

type signResponse struct {
	KeyID            string `json:"KeyId"`
	Signature        []byte `json:"Signature"`
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

type verifyRequest struct {
	KeyID            string `json:"KeyId"`
	Message          []byte `json:"Message"`
	MessageType      string `json:"MessageType"`
	Signature        []byte `json:"Signature"`
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

type verifyResponse struct {
	KeyID            string `json:"KeyId"`
	SignatureValid   bool   `json:"SignatureValid"`
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

type generateMacRequest struct {
	KeyID        string `json:"KeyId"`
	Message      []byte `json:"Message"`
	MacAlgorithm string `json:"MacAlgorithm"`
}

type generateMacResponse struct {
	KeyID        string `json:"KeyId"`
	Mac          []byte `json:"Mac"`
	MacAlgorithm string `json:"MacAlgorithm"`
}

type verifyMacRequest struct {
	KeyID        string `json:"KeyId"`
	Message      []byte `json:"Message"`
	Mac          []byte `json:"Mac"`
	MacAlgorithm string `json:"MacAlgorithm"`
}

type verifyMacResponse struct {
	KeyID        string `json:"KeyId"`
	MacValid     bool   `json:"MacValid"`
	MacAlgorithm string `json:"MacAlgorithm"`
}

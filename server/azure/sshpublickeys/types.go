package sshpublickeys

// ARM JSON shapes for Microsoft.Compute/sshPublicKeys.

type sshKeyRequest struct {
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Properties sshKeyRequestProps `json:"properties"`
}

type sshKeyRequestProps struct {
	PublicKey string `json:"publicKey,omitempty"`
}

type sshKeyResponse struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location"`
	Tags       map[string]string   `json:"tags,omitempty"`
	Properties sshKeyResponseProps `json:"properties"`
}

type sshKeyResponseProps struct {
	PublicKey string `json:"publicKey"`
}

type sshKeyListResponse struct {
	Value []sshKeyResponse `json:"value"`
}

// generateKeyPairResponse is the body returned for the GenerateKeyPair action.
type generateKeyPairResponse struct {
	ID         string `json:"id"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

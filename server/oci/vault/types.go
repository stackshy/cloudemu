package vault

import "encoding/json"

// OCI Vault and KMS REST shapes.

// definedTags is OCI's namespaced tag map. CloudEmu does not model tag
// namespaces, so a request carrying one is refused rather than silently
// stripped, and responses echo it empty.
type definedTags map[string]map[string]any

// contentTypeBase64 is the only secret content encoding OCI defines.
const contentTypeBase64 = "BASE64"

// vaultRequest is the CreateVault and UpdateVault body.
type vaultRequest struct {
	CompartmentID string            `json:"compartmentId,omitempty"`
	DisplayName   *string           `json:"displayName,omitempty"`
	VaultType     string            `json:"vaultType,omitempty"`
	FreeformTags  map[string]string `json:"freeformTags,omitempty"`
	DefinedTags   definedTags       `json:"definedTags,omitempty"`

	// Unmodelled inputs, claimed so a caller is told rather than ignored.
	RestoreFromFile            json.RawMessage `json:"restoreFromFile,omitempty"`
	RestoreFromObjectStore     json.RawMessage `json:"restoreFromObjectStore,omitempty"`
	ExternalKeyManagerMetadata json.RawMessage `json:"externalKeyManagerMetadata,omitempty"`
}

type vaultResponse struct {
	ID                 string            `json:"id"`
	CompartmentID      string            `json:"compartmentId"`
	DisplayName        string            `json:"displayName"`
	VaultType          string            `json:"vaultType"`
	CryptoEndpoint     string            `json:"cryptoEndpoint"`
	ManagementEndpoint string            `json:"managementEndpoint"`
	LifecycleState     string            `json:"lifecycleState"`
	TimeCreated        string            `json:"timeCreated"`
	TimeOfDeletion     string            `json:"timeOfDeletion,omitempty"`
	FreeformTags       map[string]string `json:"freeformTags"`
	DefinedTags        definedTags       `json:"definedTags"`
}

// keyShape is the algorithm and size of a master encryption key.
type keyShape struct {
	Algorithm string `json:"algorithm"`
	Length    int    `json:"length"`
	CurveID   string `json:"curveId,omitempty"`
}

// keyRequest is the CreateKey and UpdateKey body.
type keyRequest struct {
	CompartmentID  string            `json:"compartmentId,omitempty"`
	DisplayName    *string           `json:"displayName,omitempty"`
	KeyShape       *keyShape         `json:"keyShape,omitempty"`
	ProtectionMode string            `json:"protectionMode,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags,omitempty"`
	DefinedTags    definedTags       `json:"definedTags,omitempty"`

	// Unmodelled inputs, claimed so a caller is told rather than ignored.
	AutoKeyRotationDetails json.RawMessage `json:"autoKeyRotationDetails,omitempty"`
	ExternalKeyReference   json.RawMessage `json:"externalKeyReference,omitempty"`
	DesiredState           string          `json:"desiredState,omitempty"`
}

type keyResponse struct {
	ID                string            `json:"id"`
	CompartmentID     string            `json:"compartmentId"`
	VaultID           string            `json:"vaultId"`
	DisplayName       string            `json:"displayName"`
	KeyShape          keyShape          `json:"keyShape"`
	ProtectionMode    string            `json:"protectionMode"`
	LifecycleState    string            `json:"lifecycleState"`
	CurrentKeyVersion string            `json:"currentKeyVersion"`
	TimeCreated       string            `json:"timeCreated"`
	TimeOfDeletion    string            `json:"timeOfDeletion,omitempty"`
	FreeformTags      map[string]string `json:"freeformTags"`
	DefinedTags       definedTags       `json:"definedTags"`
}

type keyVersionResponse struct {
	ID             string `json:"id"`
	KeyID          string `json:"keyId"`
	VaultID        string `json:"vaultId"`
	CompartmentID  string `json:"compartmentId"`
	LifecycleState string `json:"lifecycleState"`
	TimeCreated    string `json:"timeCreated"`
}

// secretContent is the base64 payload a create or update writes as a new
// version.
type secretContent struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
	Name        string `json:"name,omitempty"`
	Stage       string `json:"stage,omitempty"`
}

// secretRequest is the CreateSecret and UpdateSecret body.
type secretRequest struct {
	CompartmentID        string            `json:"compartmentId,omitempty"`
	VaultID              string            `json:"vaultId,omitempty"`
	KeyID                string            `json:"keyId,omitempty"`
	SecretName           string            `json:"secretName,omitempty"`
	Description          *string           `json:"description,omitempty"`
	SecretContent        *secretContent    `json:"secretContent,omitempty"`
	CurrentVersionNumber *int64            `json:"currentVersionNumber,omitempty"`
	FreeformTags         map[string]string `json:"freeformTags,omitempty"`
	DefinedTags          definedTags       `json:"definedTags,omitempty"`

	// Unmodelled inputs, claimed so a caller is told rather than ignored.
	SecretRules      []json.RawMessage `json:"secretRules,omitempty"`
	RotationConfig   json.RawMessage   `json:"rotationConfig,omitempty"`
	SecretGeneration json.RawMessage   `json:"secretGenerationContext,omitempty"`
}

type secretResponse struct {
	ID                   string            `json:"id"`
	CompartmentID        string            `json:"compartmentId"`
	VaultID              string            `json:"vaultId"`
	KeyID                string            `json:"keyId"`
	SecretName           string            `json:"secretName"`
	Description          string            `json:"description,omitempty"`
	LifecycleState       string            `json:"lifecycleState"`
	CurrentVersionNumber int64             `json:"currentVersionNumber"`
	TimeCreated          string            `json:"timeCreated"`
	TimeOfDeletion       string            `json:"timeOfDeletion,omitempty"`
	FreeformTags         map[string]string `json:"freeformTags"`
	DefinedTags          definedTags       `json:"definedTags"`
}

type secretVersionResponse struct {
	SecretID       string   `json:"secretId"`
	VersionNumber  int64    `json:"versionNumber"`
	Name           string   `json:"name,omitempty"`
	Stages         []string `json:"stages"`
	TimeCreated    string   `json:"timeCreated"`
	TimeOfDeletion string   `json:"timeOfDeletion,omitempty"`
}

// secretBundleContent carries the version's value, base64 encoded as OCI does.
type secretBundleContent struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

type secretBundleResponse struct {
	SecretID            string              `json:"secretId"`
	VersionNumber       int64               `json:"versionNumber"`
	VersionName         string              `json:"versionName,omitempty"`
	Stages              []string            `json:"stages"`
	TimeCreated         string              `json:"timeCreated"`
	TimeOfDeletion      string              `json:"timeOfDeletion,omitempty"`
	SecretBundleContent secretBundleContent `json:"secretBundleContent"`
}

// deletionRequest is the body of every scheduleDeletion action.
type deletionRequest struct {
	TimeOfDeletion string `json:"timeOfDeletion,omitempty"`
}

// changeCompartmentRequest is the body of every changeCompartment action.
type changeCompartmentRequest struct {
	CompartmentID string `json:"compartmentId"`
}

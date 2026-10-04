package kms

// --- request bodies ---

// createCryptoKeyRequest is the CryptoKey create/patch body. Enum fields use
// rawEnum so both the string and integer wire forms decode.
type createCryptoKeyRequest struct {
	Purpose                  rawEnum              `json:"purpose"`
	VersionTemplate          *versionTemplateJSON `json:"versionTemplate"`
	Labels                   map[string]string    `json:"labels"`
	RotationPeriod           string               `json:"rotationPeriod"`
	NextRotationTime         string               `json:"nextRotationTime"`
	DestroyScheduledDuration string               `json:"destroyScheduledDuration"`
	ImportOnly               bool                 `json:"importOnly"`
	CryptoKeyBackend         string               `json:"cryptoKeyBackend"`
}

// createVersionRequest is the CryptoKeyVersion create body (state only; the
// algorithm/protectionLevel derive from the parent's versionTemplate).
type createVersionRequest struct {
	State rawEnum `json:"state"`
}

// patchVersionRequest is the CryptoKeyVersion patch body.
type patchVersionRequest struct {
	State rawEnum `json:"state"`
}

// updatePrimaryVersionRequest names the version id to promote to primary.
type updatePrimaryVersionRequest struct {
	CryptoKeyVersionID string `json:"cryptoKeyVersionId"`
}

type versionTemplateJSON struct {
	ProtectionLevel rawEnum `json:"protectionLevel"`
	Algorithm       rawEnum `json:"algorithm"`
}

// --- response bodies ---

type keyRingJSON struct {
	Name       string `json:"name"`
	CreateTime string `json:"createTime,omitempty"`
}

type versionTemplateOut struct {
	ProtectionLevel string `json:"protectionLevel,omitempty"`
	Algorithm       string `json:"algorithm,omitempty"`
}

type cryptoKeyJSON struct {
	Name                     string              `json:"name"`
	Primary                  *versionJSON        `json:"primary,omitempty"`
	Purpose                  string              `json:"purpose,omitempty"`
	CreateTime               string              `json:"createTime,omitempty"`
	NextRotationTime         string              `json:"nextRotationTime,omitempty"`
	RotationPeriod           string              `json:"rotationPeriod,omitempty"`
	VersionTemplate          *versionTemplateOut `json:"versionTemplate,omitempty"`
	Labels                   map[string]string   `json:"labels,omitempty"`
	ImportOnly               bool                `json:"importOnly,omitempty"`
	DestroyScheduledDuration string              `json:"destroyScheduledDuration,omitempty"`
	CryptoKeyBackend         string              `json:"cryptoKeyBackend,omitempty"`
}

type versionJSON struct {
	Name             string `json:"name"`
	State            string `json:"state,omitempty"`
	ProtectionLevel  string `json:"protectionLevel,omitempty"`
	Algorithm        string `json:"algorithm,omitempty"`
	CreateTime       string `json:"createTime,omitempty"`
	GenerateTime     string `json:"generateTime,omitempty"`
	DestroyTime      string `json:"destroyTime,omitempty"`
	DestroyEventTime string `json:"destroyEventTime,omitempty"`
}

type listKeyRingsResponse struct {
	KeyRings  []keyRingJSON `json:"keyRings"`
	TotalSize int           `json:"totalSize"`
}

type listCryptoKeysResponse struct {
	CryptoKeys []cryptoKeyJSON `json:"cryptoKeys"`
	TotalSize  int             `json:"totalSize"`
}

type listVersionsResponse struct {
	CryptoKeyVersions []versionJSON `json:"cryptoKeyVersions"`
	TotalSize         int           `json:"totalSize"`
}

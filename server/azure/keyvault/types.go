package keyvault

import (
	"net/http"
	"time"

	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

type attributesJSON struct {
	Enabled bool  `json:"enabled"`
	Created int64 `json:"created,omitempty"`
	Updated int64 `json:"updated,omitempty"`
}

// secretBundleJSON is the Key Vault secret bundle: the value plus its
// versioned identifier and attributes.
type secretBundleJSON struct {
	Value      string            `json:"value"`
	ID         string            `json:"id"`
	Attributes attributesJSON    `json:"attributes"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// secretItemJSON is a Key Vault list entry: identifier and attributes, no
// value.
type secretItemJSON struct {
	ID         string            `json:"id"`
	Attributes attributesJSON    `json:"attributes"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type listResponseJSON struct {
	Value    []secretItemJSON `json:"value"`
	NextLink *string          `json:"nextLink"`
}

// deletedSecretBundleJSON extends the bundle with the recovery identifier the
// SDK's DeleteSecret response carries.
type deletedSecretBundleJSON struct {
	secretBundleJSON

	RecoveryID string `json:"recoveryId"`
}

type setSecretRequest struct {
	Value string            `json:"value"`
	Tags  map[string]string `json:"tags"`
}

// epochSeconds converts an RFC3339 timestamp to Unix epoch seconds, the form
// Key Vault uses for attribute timestamps. Returns 0 on parse failure.
func epochSeconds(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return t.Unix()
}

// vaultBaseURL reconstructs the vault base URL from the request so secret
// identifiers point back at this server.
func vaultBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

// secretID builds the canonical Key Vault secret identifier
// "{vault}/secrets/{name}[/{version}]".
func secretID(r *http.Request, name, version string) string {
	id := vaultBaseURL(r) + pathPrefix + "/" + name
	if version != "" {
		id += "/" + version
	}

	return id
}

func toBundle(r *http.Request, info *secretsdriver.SecretInfo, ver *secretsdriver.SecretVersion) secretBundleJSON {
	return secretBundleJSON{
		Value: string(ver.Value),
		ID:    secretID(r, info.Name, ver.VersionID),
		Attributes: attributesJSON{
			Enabled: true,
			Created: epochSeconds(ver.CreatedAt),
			Updated: epochSeconds(ver.CreatedAt),
		},
		Tags: info.Tags,
	}
}

func toItem(r *http.Request, info *secretsdriver.SecretInfo) secretItemJSON {
	return secretItemJSON{
		ID: secretID(r, info.Name, ""),
		Attributes: attributesJSON{
			Enabled: true,
			Created: epochSeconds(info.CreatedAt),
			Updated: epochSeconds(info.UpdatedAt),
		},
		Tags: info.Tags,
	}
}

func toVersionItem(r *http.Request, name string, ver *secretsdriver.SecretVersion) secretItemJSON {
	return secretItemJSON{
		ID: secretID(r, name, ver.VersionID),
		Attributes: attributesJSON{
			Enabled: true,
			Created: epochSeconds(ver.CreatedAt),
			Updated: epochSeconds(ver.CreatedAt),
		},
	}
}

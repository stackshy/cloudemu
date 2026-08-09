package wafv2

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// apiKeyVersion is the key format version WAF reports for keys it issues.
const apiKeyVersion = 1

// CreateAPIKey issues an opaque, base64-encoded API key scoped to the given
// scope and token domains, storing it so List/Get/Delete can find it later.
func (m *Mock) CreateAPIKey(_ context.Context, scope string, tokenDomains []string) (string, error) {
	if scope == "" {
		return "", invalidParameter("Scope is required")
	}

	if len(tokenDomains) == 0 {
		return "", invalidParameter("TokenDomains is required")
	}

	apiKey := base64.StdEncoding.EncodeToString([]byte(idgen.GenerateID("apikey-")))

	m.apiKeyMu.Lock()
	defer m.apiKeyMu.Unlock()

	m.apiKeys[key(scope, apiKey)] = driver.APIKeySummary{
		APIKey:       apiKey,
		TokenDomains: copyStrings(tokenDomains),
		Version:      apiKeyVersion,
		Created:      time.Now().Unix(),
	}

	return apiKey, nil
}

// DeleteAPIKey removes an issued API key within a scope.
func (m *Mock) DeleteAPIKey(_ context.Context, scope, apiKey string) error {
	m.apiKeyMu.Lock()
	defer m.apiKeyMu.Unlock()

	k := key(scope, apiKey)
	if _, ok := m.apiKeys[k]; !ok {
		return nonexistent("API key not found in scope %s", scope)
	}

	delete(m.apiKeys, k)

	return nil
}

// ListAPIKeys returns the API keys issued within a scope.
func (m *Mock) ListAPIKeys(_ context.Context, scope string) ([]driver.APIKeySummary, error) {
	m.apiKeyMu.RLock()
	defer m.apiKeyMu.RUnlock()

	out := make([]driver.APIKeySummary, 0, len(m.apiKeys))
	prefix := scope + "/"

	for k, v := range m.apiKeys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, cloneAPIKey(v))
		}
	}

	return out, nil
}

// GetDecryptedAPIKey returns the token domains and creation time for an issued
// key, emulating WAF's decrypt of the opaque key it previously handed out.
func (m *Mock) GetDecryptedAPIKey(_ context.Context, scope, apiKey string) (*driver.APIKeySummary, error) {
	m.apiKeyMu.RLock()
	defer m.apiKeyMu.RUnlock()

	v, ok := m.apiKeys[key(scope, apiKey)]
	if !ok {
		return nil, nonexistent("API key not found in scope %s", scope)
	}

	out := cloneAPIKey(v)

	return &out, nil
}

func cloneAPIKey(in driver.APIKeySummary) driver.APIKeySummary {
	out := in
	out.TokenDomains = copyStrings(in.TokenDomains)

	return out
}

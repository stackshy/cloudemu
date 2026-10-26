package codeartifact

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// Token lifetimes in seconds: the default, and the bounds a caller may request.
const (
	defaultTokenSeconds = 12 * 60 * 60
	minTokenSeconds     = 15 * 60
	maxTokenSeconds     = 12 * 60 * 60
	tokenRandomBytes    = 48
)

// GetAuthorizationToken issues an opaque bearer token for a domain. The lifetime
// is the requested duration (0 means the 12-hour default, otherwise 900 to 43200
// seconds). The domain must exist.
func (m *Mock) GetAuthorizationToken(_ context.Context, ref driver.DomainRef, durationSeconds int64) (*driver.AuthorizationToken, error) {
	if durationSeconds != 0 && (durationSeconds < minTokenSeconds || durationSeconds > maxTokenSeconds) {
		return nil, validation("duration must be 0 or between %d and %d seconds", minTokenSeconds, maxTokenSeconds)
	}

	m.mu.RLock()
	_, err := m.domainLocked(ref)
	m.mu.RUnlock()

	if err != nil {
		return nil, err
	}

	if durationSeconds == 0 {
		durationSeconds = defaultTokenSeconds
	}

	raw := make([]byte, tokenRandomBytes)
	if _, err = rand.Read(raw); err != nil {
		return nil, fmt.Errorf("codeartifact: generate token: %w", err)
	}

	return &driver.AuthorizationToken{
		Token:      base64.RawURLEncoding.EncodeToString(raw),
		Expiration: m.now().Add(time.Duration(durationSeconds) * time.Second),
	}, nil
}

// GetRepositoryEndpoint returns the URL of a repository for one package format,
// in the shape AWS reports: https://<domain>-<owner>.d.codeartifact.<region>.amazonaws.com/<format>/<repository>/.
// The emulator does not serve that host or any registry protocol on it.
func (m *Mock) GetRepositoryEndpoint(_ context.Context, ref driver.RepositoryRef, format, endpointType string) (string, error) {
	if !validFormat(format) {
		return "", validation("package format %q is not one of npm, pypi, maven, nuget, generic, ruby, swift, cargo", format)
	}

	if endpointType != "" && endpointType != driver.EndpointTypeIPv4 && endpointType != driver.EndpointTypeDualstack {
		return "", validation("endpointType %q must be %s or %s", endpointType, driver.EndpointTypeIPv4, driver.EndpointTypeDualstack)
	}

	m.mu.RLock()
	d, _, err := m.repositoryLocked(ref)
	m.mu.RUnlock()

	if err != nil {
		return "", err
	}

	return fmt.Sprintf("https://%s-%s.d.codeartifact.%s.amazonaws.com/%s/%s/",
		d.Name, d.Owner, m.opts.Region, format, ref.Repository), nil
}

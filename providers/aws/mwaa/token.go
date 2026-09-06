package mwaa

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// CreateCliToken mints a stable Apache Airflow CLI token and the environment's
// web-server hostname. The token is a deterministic placeholder — this
// control-plane emulator does not run Apache Airflow — but it is stable across
// calls so callers can cache it. An unknown environment yields
// ResourceNotFoundException.
func (m *Mock) CreateCliToken(_ context.Context, name string) (token, webServerHostname string, err error) {
	return m.mintToken(name, "cli")
}

// CreateWebLoginToken mints a stable Apache Airflow web login token and the
// environment's web-server hostname.
func (m *Mock) CreateWebLoginToken(_ context.Context, name string) (token, webServerHostname string, err error) {
	return m.mintToken(name, "web")
}

// mintToken derives a stable token for an existing environment and returns it
// with the environment's web-server hostname.
func (m *Mock) mintToken(name, kind string) (token, webServerHostname string, err error) {
	if _, ok := m.envs.Get(name); !ok {
		return "", "", notFound(name)
	}

	sum := sha256.Sum256([]byte(kind + ":" + m.opts.AccountID + ":" + m.opts.Region + ":" + name))

	return fmt.Sprintf("%x", sum[:]), m.webserverURL(name), nil
}

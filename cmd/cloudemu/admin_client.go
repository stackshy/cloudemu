//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// adminTokenFileName is where `cloudemu start` asks serve to write the admin
// token, next to endpoints.json in the run dir.
const adminTokenFileName = "admin-token"

// adminTokenEnv overrides the token file, e.g. for a server started by hand
// with --admin-token.
const adminTokenEnv = "CLOUDEMU_ADMIN_TOKEN" //nolint:gosec // G101 false positive: an env var name, not a credential

var errAdminUnauthorized = errors.New("the server requires the admin token (it runs with --enforce-auth): " +
	"set CLOUDEMU_ADMIN_TOKEN, or start it with `cloudemu start` so the token is written to the run dir")

func adminTokenPath(dir string) string { return filepath.Join(dir, adminTokenFileName) }

// adminAPI is a running daemon's control plane: its plain-HTTP base URL and
// the admin token to send ("" when there is none, i.e. auth is off).
type adminAPI struct {
	base  string
	token string
}

// newAdminAPI resolves the daemon's control plane from the run dir. The token
// comes from CLOUDEMU_ADMIN_TOKEN, else the run dir's admin-token file.
func newAdminAPI(dir string) (adminAPI, error) {
	base, err := adminBaseURL(dir)
	if err != nil {
		return adminAPI{}, err
	}

	return adminAPI{base: base, token: adminToken(dir)}, nil
}

func adminToken(dir string) string {
	if t := strings.TrimSpace(os.Getenv(adminTokenEnv)); t != "" {
		return t
	}

	b, err := os.ReadFile(adminTokenPath(dir))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(b))
}

// adminBaseURL reads the daemon's endpoints file and returns a plain-HTTP base
// URL for the control plane (avoids the self-signed HTTPS endpoints).
func adminBaseURL(dir string) (string, error) {
	eps, err := readEndpoints(endpointsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return "", errSnapDaemonDown
	}

	if err != nil {
		return "", err
	}

	// Azure and Kubernetes are HTTPS, so this picks aws, then gcp.
	for _, k := range endpointOrder() {
		if ep := eps[k]; strings.HasPrefix(ep, "http://") {
			return strings.TrimRight(ep, "/"), nil
		}
	}

	return "", errSnapNoEndpoint
}

// do calls /_cloudemu/<path> with the admin token and returns the status and
// body. A 401 (missing or wrong token) and a 501 (control plane off) map to
// clear errors here so every subcommand reports them the same way.
func (a adminAPI) do(method, path string, body io.Reader, contentType string) (status int, respBody []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapHTTPTimeout)
	defer cancel()

	if body == nil {
		body = http.NoBody
	}

	req, err := http.NewRequestWithContext(ctx, method, a.base+"/_cloudemu/"+path, body)
	if err != nil {
		return 0, nil, err
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", errSnapDaemonDown, err)
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return resp.StatusCode, rb, errAdminUnauthorized
	case http.StatusNotImplemented:
		return resp.StatusCode, rb, errSnapAdminOff
	}

	return resp.StatusCode, rb, nil
}

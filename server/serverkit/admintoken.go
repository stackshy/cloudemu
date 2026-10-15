package serverkit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// adminTokenBytes is the entropy of a generated admin token (hex-encoded to 64
// characters).
const adminTokenBytes = 32

// adminTokenFileMode keeps the token file private to the user running serve.
const adminTokenFileMode = 0o600

// setupAdminToken picks the bearer token that gates the /_cloudemu control
// plane under EnforceAuth: the configured one, or a fresh random one. With
// AdminTokenFile set the token is written there (0600) and only the path is
// logged; otherwise a generated token is printed once on stderr so the operator
// can use it. It is a no-op when EnforceAuth or the admin plane is off, which
// keeps the documented open developer mode unchanged.
func (a *App) setupAdminToken() error {
	if !a.cfg.EnforceAuth || !a.cfg.Admin {
		return nil
	}

	token := a.cfg.AdminToken
	generated := token == ""

	if generated {
		buf := make([]byte, adminTokenBytes)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Errorf("generate admin token: %w", err)
		}

		token = hex.EncodeToString(buf)
	}

	a.adminToken = token

	if path := a.cfg.AdminTokenFile; path != "" {
		if err := writeAdminTokenFile(path, token); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "cloudemu: admin token written to %s (send it as Authorization: Bearer <token>)\n", path)

		return nil
	}

	if generated {
		fmt.Fprintf(os.Stderr, "cloudemu: admin token for /_cloudemu/*: %s (send it as Authorization: Bearer <token>)\n", token)
	}

	return nil
}

// writeAdminTokenFile writes token to path with owner-only permissions. The
// token goes into a fresh temp file in the same directory (os.CreateTemp opens
// it O_CREATE|O_EXCL with mode 0600, so nobody can pre-create it or hold it
// open), which is then renamed over path. Rename replaces whatever sits at path,
// a symlink included, without following it, so a file or link planted there in
// a shared directory can neither redirect the write nor read the token.
func writeAdminTokenFile(path, token string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write admin token file: %w", err)
	}

	tmpName := tmp.Name()

	// Remove the temp file on any failure before the rename. After a successful
	// rename it no longer exists under this name, so the Remove is a no-op.
	defer os.Remove(tmpName)

	if err := tmp.Chmod(adminTokenFileMode); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write admin token file: %w", err)
	}

	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write admin token file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write admin token file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write admin token file: %w", err)
	}

	return nil
}

package serverkit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
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
// file is removed first so an existing file with looser permissions can't keep
// them, since WriteFile only applies the mode when it creates the file.
func writeAdminTokenFile(path, token string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace admin token file: %w", err)
	}

	if err := os.WriteFile(path, []byte(token+"\n"), adminTokenFileMode); err != nil {
		return fmt.Errorf("write admin token file: %w", err)
	}

	return nil
}

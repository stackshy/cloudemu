package loganalytics

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// sharedKeysJSON is the SharedKeysClient.GetSharedKeys response shape.
type sharedKeysJSON struct {
	PrimarySharedKey   string `json:"primarySharedKey"`
	SecondarySharedKey string `json:"secondarySharedKey"`
}

// getSharedKeys serves POST .../workspaces/{w}/sharedKeys. Real Log Analytics
// returns the two base64 ingestion keys a client uses to POST logs. The keys
// are derived deterministically from the workspace so repeat calls are stable
// (until a regenerate, which the emulator does not model).
func (h *Handler) getSharedKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	info, err := h.logs.GetLogGroup(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, sharedKeysJSON{
		PrimarySharedKey:   sharedKey(info.ResourceID + "#primary"),
		SecondarySharedKey: sharedKey(info.ResourceID + "#secondary"),
	})
}

// sharedKey derives a stable, base64-encoded 512-bit shared key from a seed,
// matching the shape (88-char base64 of 64 bytes) real Log Analytics keys have.
func sharedKey(seed string) string {
	a := sha256.Sum256([]byte(seed))
	b := sha256.Sum256(a[:])
	key := append(a[:], b[:]...)

	return base64.StdEncoding.EncodeToString(key)
}

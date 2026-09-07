package backup

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// depthVaultSub is the /backup-vaults path-tail length that names a vault
// sub-resource (access policy, notifications or Vault Lock).
const depthVaultSub = 2

// serveVaults routes the /backup-vaults tree.
func (h *Handler) serveVaults(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method == http.MethodGet {
			h.listVaults(w, r)

			return
		}

		methodNotAllowed(w)
	case 1:
		h.serveVaultItem(w, r, rest[0])
	case depthVaultSub:
		h.serveVaultSub(w, r, rest[0], rest[1])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveVaultItem routes verb-keyed operations on a single vault.
func (h *Handler) serveVaultItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createVault(w, r, name)
	case http.MethodGet:
		h.describeVault(w, r, name)
	case http.MethodDelete:
		h.deleteVault(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

// serveVaultSub routes a vault sub-resource (access policy, notifications, lock).
func (h *Handler) serveVaultSub(w http.ResponseWriter, r *http.Request, name, sub string) {
	switch sub {
	case subAccessPolicy:
		h.serveAccessPolicy(w, r, name)
	case subNotifications:
		h.serveNotifications(w, r, name)
	case subVaultLock:
		h.serveVaultLock(w, r, name)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

func (h *Handler) createVault(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		BackupVaultTags  map[string]string `json:"BackupVaultTags"`
		CreatorRequestID string            `json:"CreatorRequestId"`
		EncryptionKeyArn string            `json:"EncryptionKeyArn"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	v, err := h.backup.CreateBackupVault(r.Context(), &driver.CreateVaultInput{
		Name:             name,
		Tags:             req.BackupVaultTags,
		CreatorRequestID: req.CreatorRequestID,
		EncryptionKeyArn: req.EncryptionKeyArn,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"BackupVaultArn":  v.Arn,
		"BackupVaultName": v.Name,
		"CreationDate":    epochSeconds(v.CreationDate),
	})
}

func (h *Handler) describeVault(w http.ResponseWriter, r *http.Request, name string) {
	v, err := h.backup.DescribeBackupVault(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"BackupVaultArn":         v.Arn,
		"BackupVaultName":        v.Name,
		"CreationDate":           epochSeconds(v.CreationDate),
		"NumberOfRecoveryPoints": 0,
		"Locked":                 v.Locked,
		"VaultState":             vaultStateAvailable,
		"VaultType":              vaultTypeBackup,
	}

	putString(body, "CreatorRequestId", v.CreatorRequestID)
	putString(body, "EncryptionKeyArn", v.EncryptionKeyArn)

	if v.MinRetentionDays != nil {
		body["MinRetentionDays"] = *v.MinRetentionDays
	}

	if v.MaxRetentionDays != nil {
		body["MaxRetentionDays"] = *v.MaxRetentionDays
	}

	if v.LockDate != nil {
		body["LockDate"] = epochSeconds(*v.LockDate)
	}

	writeJSON(w, body)
}

func (h *Handler) deleteVault(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.backup.DeleteBackupVault(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) listVaults(w http.ResponseWriter, r *http.Request) {
	vaults, next, err := h.backup.ListBackupVaults(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]map[string]any, 0, len(vaults))

	for _, v := range vaults {
		item := map[string]any{
			"BackupVaultArn":         v.Arn,
			"BackupVaultName":        v.Name,
			"CreationDate":           epochSeconds(v.CreationDate),
			"NumberOfRecoveryPoints": 0,
			"Locked":                 v.Locked,
		}
		putString(item, "EncryptionKeyArn", v.EncryptionKeyArn)
		putString(item, "CreatorRequestId", v.CreatorRequestID)
		list = append(list, item)
	}

	body := map[string]any{"BackupVaultList": list}
	putString(body, "NextToken", next)
	writeJSON(w, body)
}

// putString sets key to val only when val is non-empty.
func putString(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

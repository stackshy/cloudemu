package backup

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// serveAccessPolicy routes the /access-policy sub-resource.
func (h *Handler) serveAccessPolicy(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.putAccessPolicy(w, r, name)
	case http.MethodGet:
		h.getAccessPolicy(w, r, name)
	case http.MethodDelete:
		h.deleteAccessPolicy(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putAccessPolicy(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Policy string `json:"Policy"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.backup.PutBackupVaultAccessPolicy(r.Context(), name, req.Policy); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) getAccessPolicy(w http.ResponseWriter, r *http.Request, name string) {
	v, policy, err := h.backup.GetBackupVaultAccessPolicy(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"BackupVaultArn":  v.Arn,
		"BackupVaultName": v.Name,
		"Policy":          policy,
	})
}

func (h *Handler) deleteAccessPolicy(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.backup.DeleteBackupVaultAccessPolicy(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

// serveNotifications routes the /notification-configuration sub-resource.
func (h *Handler) serveNotifications(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.putNotifications(w, r, name)
	case http.MethodGet:
		h.getNotifications(w, r, name)
	case http.MethodDelete:
		h.deleteNotifications(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putNotifications(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		SNSTopicArn       string   `json:"SNSTopicArn"`
		BackupVaultEvents []string `json:"BackupVaultEvents"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	err := h.backup.PutBackupVaultNotifications(r.Context(), &driver.PutVaultNotificationsInput{
		Name:              name,
		SNSTopicArn:       req.SNSTopicArn,
		BackupVaultEvents: req.BackupVaultEvents,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) getNotifications(w http.ResponseWriter, r *http.Request, name string) {
	v, n, err := h.backup.GetBackupVaultNotifications(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"BackupVaultArn":  v.Arn,
		"BackupVaultName": v.Name,
		"SNSTopicArn":     n.SNSTopicArn,
	}
	if len(n.BackupVaultEvents) > 0 {
		body["BackupVaultEvents"] = n.BackupVaultEvents
	}

	writeJSON(w, body)
}

func (h *Handler) deleteNotifications(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.backup.DeleteBackupVaultNotifications(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

// serveVaultLock routes the /vault-lock sub-resource.
func (h *Handler) serveVaultLock(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.putVaultLock(w, r, name)
	case http.MethodDelete:
		h.deleteVaultLock(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putVaultLock(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		MinRetentionDays  *int64 `json:"MinRetentionDays"`
		MaxRetentionDays  *int64 `json:"MaxRetentionDays"`
		ChangeableForDays *int64 `json:"ChangeableForDays"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	err := h.backup.PutBackupVaultLockConfiguration(r.Context(), &driver.PutVaultLockInput{
		Name:              name,
		MinRetentionDays:  req.MinRetentionDays,
		MaxRetentionDays:  req.MaxRetentionDays,
		ChangeableForDays: req.ChangeableForDays,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) deleteVaultLock(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.backup.DeleteBackupVaultLockConfiguration(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

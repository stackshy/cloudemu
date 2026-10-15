package storageaccount

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	tabledriver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

// WithQueues backs …/queueServices/default/queues with the Queue Storage data
// plane, so a queue created over ARM is the same queue the data plane serves.
func (h *Handler) WithQueues(mq mqdriver.MessageQueue) *Handler {
	h.queues = mq
	return h
}

// WithTables backs …/tableServices/default/tables with the Table Storage data
// plane.
func (h *Handler) WithTables(ts tabledriver.TableStorage) *Handler {
	h.tables = ts
	return h
}

// armQueue is the ARM StorageQueue / ListQueue wire shape.
type armQueue struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Properties armQueueProperties `json:"properties"`
}

type armQueueProperties struct {
	Metadata                map[string]string `json:"metadata,omitempty"`
	ApproximateMessageCount *int              `json:"approximateMessageCount,omitempty"`
}

// armTable is the ARM Table wire shape.
type armTable struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Properties armTableProperties `json:"properties"`
}

type armTableProperties struct {
	TableName         string              `json:"tableName"`
	SignedIdentifiers []armTableSignedIDs `json:"signedIdentifiers,omitempty"`
}

type armTableSignedIDs struct {
	ID           string                `json:"id"`
	AccessPolicy *armTableAccessPolicy `json:"accessPolicy,omitempty"`
}

type armTableAccessPolicy struct {
	StartTime  string `json:"startTime,omitempty"`
	ExpiryTime string `json:"expiryTime,omitempty"`
	Permission string `json:"permission"`
}

// toStoredACL converts ARM signedIdentifiers to the stored form the Table data
// plane's Get/Set Table ACL shares.
func toStoredACL(in []armTableSignedIDs) []storagedriver.SignedIdentifier {
	out := make([]storagedriver.SignedIdentifier, 0, len(in))

	for _, s := range in {
		id := storagedriver.SignedIdentifier{ID: s.ID}
		if p := s.AccessPolicy; p != nil {
			id.Start, id.Expiry, id.Permission = p.StartTime, p.ExpiryTime, p.Permission
		}

		out = append(out, id)
	}

	return out
}

func fromStoredACL(in []storagedriver.SignedIdentifier) []armTableSignedIDs {
	out := make([]armTableSignedIDs, 0, len(in))

	for _, s := range in {
		out = append(out, armTableSignedIDs{
			ID:           s.ID,
			AccessPolicy: &armTableAccessPolicy{StartTime: s.Start, ExpiryTime: s.Expiry, Permission: s.Permission},
		})
	}

	return out
}

// serveQueueChild serves …/queueServices/default/queues[/{name}].
func (h *Handler) serveQueueChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.Rest == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listARMQueues(w, r, rp)

		return
	}

	key := storagedriver.AzureContainerKey(rp.ResourceName, rp.Rest)

	switch r.Method {
	case http.MethodGet:
		h.writeARMQueue(w, r, rp, key)
	case http.MethodPut, http.MethodPatch:
		h.putARMQueue(w, r, rp, key)
	case http.MethodDelete:
		if url, ok := h.queueURL(r.Context(), key); ok {
			if err := h.queues.DeleteQueue(r.Context(), url); err != nil {
				azurearm.WriteCErr(w, err)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putARMQueue(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, key string) {
	if !validStorageName(rp.Rest, true) {
		azurearm.WriteError(w, http.StatusBadRequest, "QueueNameInvalid",
			"Queue name must be 3-63 lowercase letters, numbers or hyphens.")

		return
	}

	var body struct {
		Properties armQueueProperties `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	url, exists := h.queueURL(r.Context(), key)
	if !exists {
		_, err := h.queues.CreateQueue(r.Context(), mqdriver.QueueConfig{Name: key, Metadata: body.Properties.Metadata})
		if err == nil {
			h.writeARMQueue(w, r, rp, key)
			return
		}

		// A concurrent PUT created it first; update it like any existing queue.
		if !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		if url, exists = h.queueURL(r.Context(), key); !exists {
			azurearm.WriteError(w, http.StatusNotFound, "QueueNotFound", "The specified queue does not exist.")
			return
		}
	}

	// PATCH leaves metadata alone unless the body carries it.
	if r.Method == http.MethodPatch && body.Properties.Metadata == nil {
		h.writeARMQueue(w, r, rp, key)
		return
	}

	if svc, ok := h.queues.(mqdriver.AzureQueueStorage); ok {
		if err := svc.SetQueueMetadata(r.Context(), url, body.Properties.Metadata); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	h.writeARMQueue(w, r, rp, key)
}

func (h *Handler) writeARMQueue(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, key string) {
	url, ok := h.queueURL(r.Context(), key)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "QueueNotFound", "The specified queue does not exist.")
		return
	}

	q := armQueue{
		ID:   childID(rp, "queueServices/default/queues", rp.Rest),
		Name: rp.Rest,
		Type: providerName + "/" + resourceType + "/queueServices/queues",
	}

	if svc, isAzure := h.queues.(mqdriver.AzureQueueStorage); isAzure {
		if meta, err := svc.GetQueueMetadata(r.Context(), url); err == nil {
			count := meta.ApproximateMessageCount
			q.Properties = armQueueProperties{Metadata: meta.Metadata, ApproximateMessageCount: &count}
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, q)
}

func (h *Handler) listARMQueues(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	queues, err := h.queues.ListQueues(r.Context(), "")
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := []armQueue{}

	for _, qi := range queues {
		if name, ok := inAccount(qi.Name, rp.ResourceName); ok {
			q := armQueue{
				ID:   childID(rp, "queueServices/default/queues", name),
				Name: name,
				Type: providerName + "/" + resourceType + "/queueServices/queues",
			}

			if svc, isAzure := h.queues.(mqdriver.AzureQueueStorage); isAzure {
				if meta, err := svc.GetQueueMetadata(r.Context(), qi.URL); err == nil {
					q.Properties = armQueueProperties{Metadata: meta.Metadata}
				}
			}

			out = append(out, q)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, armValueList{Value: out})
}

// queueURL resolves a queue key to the driver's queue URL.
func (h *Handler) queueURL(ctx context.Context, key string) (string, bool) {
	queues, err := h.queues.ListQueues(ctx, "")
	if err != nil {
		return "", false
	}

	for _, qi := range queues {
		if qi.Name == key {
			return qi.URL, true
		}
	}

	return "", false
}

// serveTableChild serves …/tableServices/default/tables[/{name}].
func (h *Handler) serveTableChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.Rest == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listARMTables(w, r, rp)

		return
	}

	key := storagedriver.AzureContainerKey(rp.ResourceName, rp.Rest)

	switch r.Method {
	case http.MethodGet:
		h.writeARMTable(w, r, rp, key)
	case http.MethodPut, http.MethodPatch:
		h.putARMTable(w, r, rp, key)
	case http.MethodDelete:
		if err := h.tables.DeleteTable(r.Context(), key); err != nil && !cerrors.IsNotFound(err) {
			azurearm.WriteCErr(w, err)
			return
		}

		_, _ = h.settings.DeleteAccountSetting(r.Context(), rp.ResourceName, storagedriver.AzureTableACLKind(rp.Rest))

		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putARMTable(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, key string) {
	if !validStorageName(rp.Rest, false) {
		azurearm.WriteError(w, http.StatusBadRequest, "OutOfRangeInput",
			"Table name must be 3-63 letters or numbers and start with a letter.")

		return
	}

	var body struct {
		Properties armTableProperties `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if !h.tableExists(r.Context(), key) {
		// AlreadyExists means a concurrent PUT created it first.
		if err := h.tables.CreateTable(r.Context(), key); err != nil && !cerrors.IsAlreadyExists(err) {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	// PATCH leaves the stored access policies alone unless the body carries them.
	if r.Method == http.MethodPatch && body.Properties.SignedIdentifiers == nil {
		h.writeARMTable(w, r, rp, key)
		return
	}

	acl, _ := json.Marshal(toStoredACL(body.Properties.SignedIdentifiers))
	if _, err := h.settings.SetAccountSetting(r.Context(), rp.ResourceName, storagedriver.AzureTableACLKind(rp.Rest), acl); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeARMTable(w, r, rp, key)
}

func (h *Handler) writeARMTable(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, key string) {
	if !h.tableExists(r.Context(), key) {
		azurearm.WriteError(w, http.StatusNotFound, "TableNotFound", "The table specified does not exist.")
		return
	}

	t := h.armTableOf(rp, rp.Rest)

	if stored, ok, err := h.settings.AccountSetting(r.Context(), rp.ResourceName, storagedriver.AzureTableACLKind(rp.Rest)); err == nil && ok {
		var ids []storagedriver.SignedIdentifier
		if json.Unmarshal(stored.Properties, &ids) == nil && len(ids) > 0 {
			t.Properties.SignedIdentifiers = fromStoredACL(ids)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) listARMTables(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	names, err := h.tables.ListTables(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := []armTable{}

	for _, n := range names {
		if name, ok := inAccount(n, rp.ResourceName); ok {
			out = append(out, h.armTableOf(rp, name))
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, armValueList{Value: out})
}

func (*Handler) armTableOf(rp *azurearm.ResourcePath, name string) armTable {
	return armTable{
		ID:         childID(rp, "tableServices/default/tables", name),
		Name:       name,
		Type:       providerName + "/" + resourceType + "/tableServices/tables",
		Properties: armTableProperties{TableName: name},
	}
}

func (h *Handler) tableExists(ctx context.Context, key string) bool {
	names, err := h.tables.ListTables(ctx)
	if err != nil {
		return false
	}

	for _, n := range names {
		if n == key {
			return true
		}
	}

	return false
}

// purgeQueuesAndTables deletes every queue and table of account, backing the
// account delete and the resource-group cascade.
func (h *Handler) purgeQueuesAndTables(ctx context.Context, account string) error {
	if err := h.purgeQueues(ctx, account); err != nil {
		return err
	}

	if h.tables == nil {
		return nil
	}

	names, err := h.tables.ListTables(ctx)
	if err != nil {
		return err
	}

	for _, n := range names {
		if _, ok := inAccount(n, account); ok {
			if err := h.tables.DeleteTable(ctx, n); err != nil && !cerrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

func (h *Handler) purgeQueues(ctx context.Context, account string) error {
	if h.queues == nil {
		return nil
	}

	queues, err := h.queues.ListQueues(ctx, "")
	if err != nil {
		return err
	}

	for _, qi := range queues {
		if _, ok := inAccount(qi.Name, account); !ok {
			continue
		}

		if err := h.queues.DeleteQueue(ctx, qi.URL); err != nil && !cerrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// inAccount reports whether a queue or table key belongs to account and, if
// so, its own name. The default account "cloudemu" owns the bare keys.
func inAccount(key, account string) (string, bool) {
	acct, name := storagedriver.SplitAzureContainerKey(key)
	if acct == "" {
		acct = storagedriver.AzureDefaultStorageAccount
	}

	return name, strings.EqualFold(acct, account)
}

// queueNamePattern and tableNamePattern are the Azure naming rules: a queue
// is lowercase letters, digits and single hyphens, starting and ending with a
// letter or digit; a table is letters and digits starting with a letter.
// Both are 3-63 characters (checked separately for queues).
var (
	queueNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9]|-[a-z0-9])+$`)
	tableNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{2,62}$`)
)

// validStorageName checks a queue name (queue true) or a table name.
func validStorageName(name string, queue bool) bool {
	const minLen, maxLen = 3, 63

	if !queue {
		return tableNamePattern.MatchString(name)
	}

	return len(name) >= minLen && len(name) <= maxLen && queueNamePattern.MatchString(name)
}

// armValueList is the {"value":[…]} envelope of an ARM list.
type armValueList struct {
	Value any `json:"value"`
}

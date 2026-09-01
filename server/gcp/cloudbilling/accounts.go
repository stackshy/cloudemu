package cloudbilling

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// accountSeqStart offsets generated account ids past the seeded account so a
// created account never collides with the seed.
const accountSeqStart = 100

// serveAccountCollection dispatches /v1/billingAccounts.
func (h *Handler) serveAccountCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listAccounts(w, r)
	case http.MethodPost:
		h.createAccount(w, r)
	default:
		writeUnsupported(w)
	}
}

// serveAccount dispatches /v1/billingAccounts/{id}.
func (h *Handler) serveAccount(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getAccount(w, rt)
	case http.MethodPatch:
		h.patchAccount(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	all := h.snapshotAccounts()

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	page, next := paginate(all, r)
	gcprest.WriteJSON(w, http.StatusOK, listBillingAccountsResponse{BillingAccounts: page, NextPageToken: next})
}

// snapshotAccounts returns a copy of every billing account under the read lock.
func (h *Handler) snapshotAccounts() []*billingAccount {
	h.mu.RLock()
	defer h.mu.RUnlock()

	all := make([]*billingAccount, 0, len(h.accounts))

	for _, a := range h.accounts {
		all = append(all, a)
	}

	return all
}

func (h *Handler) getAccount(w http.ResponseWriter, rt route) {
	h.mu.RLock()
	acct, ok := h.accounts[rt.accountID]
	h.mu.RUnlock()

	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "billing account not found: "+rt.accountID)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, acct)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var in billingAccount
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	h.mu.Lock()
	h.accountSeq++
	n := accountSeqStart + h.accountSeq
	id := fmt.Sprintf("%06X-%06X-%06X", n, n, n)
	acct := &billingAccount{
		Name:                 billingAccountsName + id,
		Open:                 true,
		DisplayName:          in.DisplayName,
		MasterBillingAccount: in.MasterBillingAccount,
		CurrencyCode:         in.CurrencyCode,
	}
	h.accounts[id] = acct
	h.mu.Unlock()

	gcprest.WriteJSON(w, http.StatusOK, acct)
}

func (h *Handler) patchAccount(w http.ResponseWriter, r *http.Request, rt route) {
	var in billingAccount
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	acct, ok := h.accounts[rt.accountID]
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "billing account not found: "+rt.accountID)
		return
	}

	if in.DisplayName != "" {
		acct.DisplayName = in.DisplayName
	}

	gcprest.WriteJSON(w, http.StatusOK, acct)
}

// serveAccountProjects lists the projects linked to a billing account.
func (h *Handler) serveAccountProjects(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodGet {
		writeUnsupported(w)
		return
	}

	linked, ok := h.linkedProjects(rt.accountID)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "billing account not found: "+rt.accountID)
		return
	}

	sort.Slice(linked, func(i, j int) bool { return linked[i].ProjectID < linked[j].ProjectID })

	page, next := paginate(linked, r)
	gcprest.WriteJSON(w, http.StatusOK, listProjectBillingInfoResponse{ProjectBillingInfo: page, NextPageToken: next})
}

// linkedProjects returns the project-billing records linked to accountID. ok is
// false when the account does not exist.
func (h *Handler) linkedProjects(accountID string) ([]*projectBillingInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if _, ok := h.accounts[accountID]; !ok {
		return nil, false
	}

	name := billingAccountsName + accountID

	var linked []*projectBillingInfo

	for _, pi := range h.projectInfo {
		if pi.BillingAccountName == name {
			linked = append(linked, pi)
		}
	}

	return linked, true
}

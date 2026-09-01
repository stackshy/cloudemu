package cloudbilling

import (
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// serveBudgetCollection dispatches /v1/billingAccounts/{id}/budgets.
func (h *Handler) serveBudgetCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.listBudgets(w, r, rt)
	case http.MethodPost:
		h.createBudget(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveBudget dispatches /v1/billingAccounts/{id}/budgets/{budgetId}.
func (h *Handler) serveBudget(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getBudget(w, rt)
	case http.MethodPatch:
		h.patchBudget(w, r, rt)
	case http.MethodDelete:
		h.deleteBudget(w, rt)
	default:
		writeUnsupported(w)
	}
}

func budgetKey(accountID, budgetID string) string {
	return accountID + "/" + budgetID
}

func (h *Handler) createBudget(w http.ResponseWriter, r *http.Request, rt route) {
	var in budget
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.accounts[rt.accountID]; !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "billing account not found: "+rt.accountID)
		return
	}

	budgetID := idgen.UUID()
	in.Name = billingAccountsName + rt.accountID + "/budgets/" + budgetID
	in.Etag = idgen.UUID()
	h.budgets[budgetKey(rt.accountID, budgetID)] = &in

	gcprest.WriteJSON(w, http.StatusOK, &in)
}

func (h *Handler) listBudgets(w http.ResponseWriter, r *http.Request, rt route) {
	all, ok := h.budgetsForAccount(rt.accountID)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "billing account not found: "+rt.accountID)
		return
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	page, next := paginate(all, r)
	gcprest.WriteJSON(w, http.StatusOK, listBudgetsResponse{Budgets: page, NextPageToken: next})
}

// budgetsForAccount returns the budgets owned by accountID. ok is false when
// the account does not exist.
func (h *Handler) budgetsForAccount(accountID string) ([]*budget, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if _, ok := h.accounts[accountID]; !ok {
		return nil, false
	}

	prefix := accountID + "/"

	var all []*budget

	for key, b := range h.budgets {
		if strings.HasPrefix(key, prefix) {
			all = append(all, b)
		}
	}

	return all, true
}

func (h *Handler) getBudget(w http.ResponseWriter, rt route) {
	h.mu.RLock()
	b, ok := h.budgets[budgetKey(rt.accountID, rt.budgetID)]
	h.mu.RUnlock()

	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "budget not found: "+rt.budgetID)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) patchBudget(w http.ResponseWriter, r *http.Request, rt route) {
	var in budget
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	b, ok := h.budgets[budgetKey(rt.accountID, rt.budgetID)]
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "budget not found: "+rt.budgetID)
		return
	}

	applyBudgetPatch(b, &in)
	b.Etag = idgen.UUID()

	gcprest.WriteJSON(w, http.StatusOK, b)
}

// applyBudgetPatch overlays the non-empty fields of in onto b. The name and
// etag are server-owned and never taken from the request.
func applyBudgetPatch(b, in *budget) {
	if in.DisplayName != "" {
		b.DisplayName = in.DisplayName
	}

	if in.Amount != nil {
		b.Amount = in.Amount
	}

	if in.BudgetFilter != nil {
		b.BudgetFilter = in.BudgetFilter
	}

	if in.ThresholdRules != nil {
		b.ThresholdRules = in.ThresholdRules
	}

	if in.NotificationsRule != nil {
		b.NotificationsRule = in.NotificationsRule
	}

	if in.OwnershipScope != "" {
		b.OwnershipScope = in.OwnershipScope
	}
}

func (h *Handler) deleteBudget(w http.ResponseWriter, rt route) {
	h.mu.Lock()
	key := budgetKey(rt.accountID, rt.budgetID)
	_, ok := h.budgets[key]
	delete(h.budgets, key)
	h.mu.Unlock()

	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "budget not found: "+rt.budgetID)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

package sesv2

import (
	"net/http"
)

// serveAccount routes /account and its sub-paths.
func (h *Handler) serveAccount(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method == http.MethodGet {
			h.getAccount(w, r)

			return
		}

		methodNotAllowed(w)
	case 1:
		h.serveAccountSub(w, r, rest[0])
	default:
		notFound(w, r.URL.Path)
	}
}

// serveAccountSub routes /account/{sending,suppression} (both PUT-only).
func (h *Handler) serveAccountSub(w http.ResponseWriter, r *http.Request, sub string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	switch sub {
	case "sending":
		h.putAccountSending(w, r)
	case "suppression":
		h.putAccountSuppression(w, r)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	acct, err := h.ses.GetAccount(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getAccountResponse{
		SendingEnabled:          acct.SendingEnabled,
		ProductionAccessEnabled: acct.ProductionAccessEnabled,
		EnforcementStatus:       acct.EnforcementStatus,
		SendQuota: sendQuotaJSON{
			Max24HourSend:   acct.Max24HourSend,
			MaxSendRate:     acct.MaxSendRate,
			SentLast24Hours: acct.SentLast24Hours,
		},
		SuppressionAttributes: suppressionAttributesJSON{SuppressedReasons: acct.SuppressedReasons},
	})
}

func (h *Handler) putAccountSending(w http.ResponseWriter, r *http.Request) {
	var req putAccountSendingRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.ses.PutAccountSendingAttributes(r.Context(), req.SendingEnabled); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) putAccountSuppression(w http.ResponseWriter, r *http.Request) {
	var req putAccountSuppressionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.ses.PutAccountSuppressionAttributes(r.Context(), req.SuppressedReasons); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

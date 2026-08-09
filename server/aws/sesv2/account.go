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
	case twoSegments:
		h.serveAccountDedicatedIps(w, r, rest)
	default:
		notFound(w, r.URL.Path)
	}
}

// serveAccountSub routes /account/{sending,suppression,vdm,pricing-attributes}
// (PUT) and /account/details (POST).
func (h *Handler) serveAccountSub(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == "details" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		h.putAccountDetails(w, r)

		return
	}

	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	switch sub {
	case segSending:
		h.putAccountSending(w, r)
	case "suppression":
		h.putAccountSuppression(w, r)
	case "vdm":
		h.putAccountVdm(w, r)
	case "pricing-attributes":
		writeOK(w, h.ses.PutAccountPricingAttributes(r.Context()))
	default:
		notFound(w, r.URL.Path)
	}
}

// serveAccountDedicatedIps routes /account/dedicated-ips/warmup (PUT).
func (h *Handler) serveAccountDedicatedIps(w http.ResponseWriter, r *http.Request, rest []string) {
	if rest[0] != "dedicated-ips" || rest[1] != segWarmup || r.Method != http.MethodPut {
		notFound(w, r.URL.Path)

		return
	}

	var req putAccountWarmupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutAccountDedicatedIPWarmupAttributes(r.Context(), req.AutoWarmupEnabled))
}

func (h *Handler) putAccountDetails(w http.ResponseWriter, r *http.Request) {
	var req putAccountDetailsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutAccountDetails(r.Context(), req.MailType, req.WebsiteURL, req.ProductionAccessEnabled))
}

func (h *Handler) putAccountVdm(w http.ResponseWriter, r *http.Request) {
	var req putAccountVdmRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	enabled := req.VdmAttributes != nil && req.VdmAttributes.VdmEnabled == "ENABLED"
	writeOK(w, h.ses.PutAccountVdmAttributes(r.Context(), enabled))
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

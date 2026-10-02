package apigateway

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

type throttleSettings struct {
	BurstLimit int     `json:"burstLimit"`
	RateLimit  float64 `json:"rateLimit"`
}

// accountResponse is the Account wire object.
type accountResponse struct {
	CloudWatchRoleARN string           `json:"cloudwatchRoleArn,omitempty"`
	ThrottleSettings  throttleSettings `json:"throttleSettings"`
	Features          []string         `json:"features"`
	APIKeyVersion     string           `json:"apiKeyVersion"`
}

func toAccountResponse(a *driver.Account) accountResponse {
	return accountResponse{
		CloudWatchRoleARN: a.CloudWatchRoleARN,
		ThrottleSettings:  throttleSettings{BurstLimit: a.Throttle.BurstLimit, RateLimit: a.Throttle.RateLimit},
		Features:          a.Features, APIKeyVersion: a.APIKeyVersion,
	}
}

// serveAccount handles /account: GET=GetAccount, PATCH=UpdateAccount.
func (h *Handler) serveAccount(w http.ResponseWriter, r *http.Request) {
	if servePatch(w, r,
		func(ops []driver.PatchOperation) (*driver.Account, error) {
			return h.ag.UpdateAccount(r.Context(), ops)
		},
		toAccountResponse,
	) {
		return
	}

	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	acct, err := h.ag.GetAccount(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toAccountResponse(acct))
}

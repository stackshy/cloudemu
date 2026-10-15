package lambda

import (
	"context"
	"net/http"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// accountSettingsPath is the GetAccountSettings route. See
// https://docs.aws.amazon.com/lambda/latest/api/API_GetAccountSettings.html
const accountSettingsPath = "/2016-08-19/account-settings"

// accountSettingsReader is the AWS-only GetAccountSettings surface, type-asserted
// like the other AWS-only Lambda capabilities.
type accountSettingsReader interface {
	GetAccountSettings(ctx context.Context) (*sdrv.AccountSettings, error)
}

type accountLimitJSON struct {
	TotalCodeSize                  int64 `json:"TotalCodeSize"`
	CodeSizeUnzipped               int64 `json:"CodeSizeUnzipped"`
	CodeSizeZipped                 int64 `json:"CodeSizeZipped"`
	ConcurrentExecutions           int   `json:"ConcurrentExecutions"`
	UnreservedConcurrentExecutions int   `json:"UnreservedConcurrentExecutions"`
}

type accountUsageJSON struct {
	TotalCodeSize int64 `json:"TotalCodeSize"`
	FunctionCount int64 `json:"FunctionCount"`
}

// serveAccountSettings handles GET /2016-08-19/account-settings.
func (h *Handler) serveAccountSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	reader, ok := h.fn.(accountSettingsReader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "account settings not supported")
		return
	}

	s, err := reader.GetAccountSettings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"AccountLimit": accountLimitJSON{
			TotalCodeSize:                  s.TotalCodeSizeLimit,
			CodeSizeUnzipped:               s.CodeSizeUnzipped,
			CodeSizeZipped:                 s.CodeSizeZipped,
			ConcurrentExecutions:           s.ConcurrentExecutions,
			UnreservedConcurrentExecutions: s.UnreservedConcurrentExecutions,
		},
		"AccountUsage": accountUsageJSON{TotalCodeSize: s.TotalCodeSizeUsed, FunctionCount: s.FunctionCount},
	})
}

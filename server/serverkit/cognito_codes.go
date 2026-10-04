package serverkit

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	cognitodriver "github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// cognitoCodesPath is the admin endpoint that reads back the confirmation code
// the emulator would have emailed or texted to a Cognito user:
// GET /_cloudemu/cognito/codes?userPoolId=<pool>&username=<name>.
const cognitoCodesPath = "cognito/codes"

type cognitoCodeJSON struct {
	UserPoolID     string `json:"userPoolId"`
	Username       string `json:"username"`
	Code           string `json:"code"`
	ExpiresAt      string `json:"expiresAt"`
	DeliveryMedium string `json:"deliveryMedium,omitempty"`
	Destination    string `json:"destination,omitempty"`
	AttributeName  string `json:"attributeName,omitempty"`
}

// serveCognitoCodes answers the codes endpoint from the live AWS regions.
func (a *App) serveCognitoCodes(w http.ResponseWriter, r *http.Request) {
	a.rebuildMu.Lock()
	mux := a.awsMux
	a.rebuildMu.Unlock()

	if mux == nil {
		writeNetErr(w, http.StatusServiceUnavailable, "cognito codes require the aws provider")

		return
	}

	serveCognitoCode(w, r, mux.LiveProviders())
}

// serveCognitoCode looks the code up in the region the pool id names
// ("<region>_<suffix>").
func serveCognitoCode(w http.ResponseWriter, r *http.Request, regions map[string]*awsprovider.Provider) {
	if r.Method != http.MethodGet {
		writeNetErr(w, http.StatusMethodNotAllowed, "cognito/codes requires GET")

		return
	}

	poolID := r.URL.Query().Get("userPoolId")
	username := r.URL.Query().Get("username")

	region, _, ok := strings.Cut(poolID, "_")
	if !ok || region == "" || username == "" {
		writeNetErr(w, http.StatusBadRequest, "userPoolId and username are required")

		return
	}

	prov, ok := regions[region]
	if !ok {
		writeNetErr(w, http.StatusNotFound, "User pool "+poolID+" does not exist.")

		return
	}

	code, err := prov.Cognito.ConfirmationCode(r.Context(), poolID, username)
	if err != nil {
		status := http.StatusInternalServerError

		var apiErr *cognitodriver.APIError
		if errors.As(err, &apiErr) {
			status = http.StatusNotFound
		}

		writeNetErr(w, status, cerrors.Message(err))

		return
	}

	out := cognitoCodeJSON{
		UserPoolID: poolID,
		Username:   username,
		Code:       code.Code,
		ExpiresAt:  code.ExpiresAt.UTC().Format(time.RFC3339),
	}

	if d := code.Delivery; d != nil {
		out.DeliveryMedium, out.Destination, out.AttributeName = d.DeliveryMedium, d.Destination, d.AttributeName
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

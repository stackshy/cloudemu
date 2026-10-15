package bedrockagent

import (
	"encoding/json"
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// segValidateDefinition is the /flows/validate-definition path segment. Flow
// ids are 10 characters, so it never collides with a flow id.
const segValidateDefinition = "validate-definition"

type validateFlowRequest struct {
	Definition json.RawMessage `json:"definition"`
}

type flowValidationJSON struct {
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Type     string `json:"type,omitempty"`
}

type validateFlowResponse struct {
	Validations []flowValidationJSON `json:"validations"`
}

func validateFlowDefinition(w http.ResponseWriter, r *http.Request, c badriver.FlowValidator) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var in validateFlowRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	found, err := c.ValidateFlowDefinition(r.Context(), in.Definition)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]flowValidationJSON, 0, len(found))
	for _, v := range found {
		out = append(out, flowValidationJSON{Message: v.Message, Severity: v.Severity, Type: v.Type})
	}

	writeJSON(w, validateFlowResponse{Validations: out})
}

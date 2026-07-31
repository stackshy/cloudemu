package keyspaces

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// serveOptional handles the auto-scaling operation, an optional driver
// capability discovered by type assertion.
func (h *Handler) serveOptional(w http.ResponseWriter, r *http.Request, op string) bool {
	if op != "GetTableAutoScalingSettings" {
		return false
	}

	as, ok := h.db.(ksdriver.AutoScaling)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", "auto scaling not supported")
		return true
	}

	var in keyspaces.GetTableAutoScalingSettingsInput
	if !wire.DecodeJSON(w, r, &in) {
		return true
	}

	t, err := as.GetTableAutoScalingSettings(r.Context(), aws.ToString(in.KeyspaceName), aws.ToString(in.TableName))
	if err != nil {
		writeErr(w, err)
		return true
	}

	writeJSON(w, keyspaces.GetTableAutoScalingSettingsOutput{
		KeyspaceName:             aws.String(t.KeyspaceName),
		TableName:                aws.String(t.Name),
		ResourceArn:              aws.String(t.ARN),
		AutoScalingSpecification: toWireAutoScaling(t.AutoScaling),
	})

	return true
}

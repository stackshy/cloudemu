package sqlvirtualmachine

import "github.com/stackshy/cloudemu/v2/providers/azure/sqlvirtualmachine"

// armResource is the ARM wire representation of a SQL virtual machine, shared by
// the PUT request body and every response. The properties block is the provider
// record's Properties type verbatim — it already carries the exact ARM JSON tags
// — so the writable request shape and the read-back response shape stay in sync.
type armResource struct {
	ID         string                        `json:"id,omitempty"`
	Name       string                        `json:"name,omitempty"`
	Type       string                        `json:"type,omitempty"`
	Location   string                        `json:"location,omitempty"`
	Tags       map[string]string             `json:"tags,omitempty"`
	Properties *sqlvirtualmachine.Properties `json:"properties,omitempty"`
}

// updateRequest is the ARM PATCH body (armsqlvirtualmachine SQLVirtualMachineUpdate).
// Only tags are updatable through PATCH; they replace the existing set wholesale.
type updateRequest struct {
	Tags map[string]string `json:"tags"`
}

// listResponse is the ARM list envelope for ListByResourceGroup / List.
// nextLink is omitted — the emulator returns a single page.
type listResponse struct {
	Value []armResource `json:"value"`
}

// toResponse projects a stored record onto its ARM wire representation.
func toResponse(rec *sqlvirtualmachine.Record) armResource {
	props := rec.Properties

	return armResource{
		ID:         rec.ARMID(),
		Name:       rec.Name,
		Type:       armType,
		Location:   rec.Location,
		Tags:       rec.Tags,
		Properties: &props,
	}
}

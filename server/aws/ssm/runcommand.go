package ssm

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

type sendCommandRequest struct {
	InstanceIds  []string            `json:"InstanceIds"`
	DocumentName string              `json:"DocumentName"`
	Comment      string              `json:"Comment"`
	Parameters   map[string][]string `json:"Parameters"`
}

type commandJSON struct {
	CommandId    string   `json:"CommandId"`
	DocumentName string   `json:"DocumentName"`
	Status       string   `json:"Status"`
	InstanceIds  []string `json:"InstanceIds"`
	Comment      string   `json:"Comment,omitempty"`
}

type sendCommandResponse struct {
	Command commandJSON `json:"Command"`
}

type getCommandInvocationRequest struct {
	CommandId  string `json:"CommandId"`
	InstanceId string `json:"InstanceId"`
}

type getCommandInvocationResponse struct {
	CommandId             string `json:"CommandId"`
	InstanceId            string `json:"InstanceId"`
	DocumentName          string `json:"DocumentName"`
	Status                string `json:"Status"`
	StatusDetails         string `json:"StatusDetails"`
	ResponseCode          int32  `json:"ResponseCode"`
	StandardOutputContent string `json:"StandardOutputContent"`
	StandardErrorContent  string `json:"StandardErrorContent"`
}

// runCommand reports whether the configured driver supports Run Command.
func (h *Handler) runCommand() (ssmdriver.RunCommand, bool) {
	rc, ok := h.store.(ssmdriver.RunCommand)

	return rc, ok
}

func (h *Handler) sendCommand(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand()
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnsupportedOperationException", "this driver does not support Run Command")

		return
	}

	var req sendCommandRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	commandID, err := store.SendCommand(r.Context(), ssmdriver.CommandConfig{
		InstanceIDs:  req.InstanceIds,
		DocumentName: req.DocumentName,
		Comment:      req.Comment,
		Parameters:   req.Parameters,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// Real SSM reports the command as Pending here — it has been accepted, not
	// finished — and the caller learns the outcome from GetCommandInvocation.
	// Reporting Success would invite a caller to skip the poll it would need
	// against the real service.
	wire.WriteJSON(w, sendCommandResponse{Command: commandJSON{
		CommandId:    commandID,
		DocumentName: req.DocumentName,
		Status:       "Pending",
		InstanceIds:  req.InstanceIds,
		Comment:      req.Comment,
	}})
}

func (h *Handler) getCommandInvocation(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand()
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnsupportedOperationException", "this driver does not support Run Command")

		return
	}

	var req getCommandInvocationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	inv, err := store.GetCommandInvocation(r.Context(), req.CommandId, req.InstanceId)
	if err != nil {
		// AWS names this one specifically, and callers branch on it while
		// polling a command that has not registered yet.
		wire.WriteJSONError(w, http.StatusBadRequest, "InvocationDoesNotExist", err.Error())

		return
	}

	wire.WriteJSON(w, getCommandInvocationResponse{
		CommandId:             inv.CommandID,
		InstanceId:            inv.InstanceID,
		DocumentName:          inv.DocumentName,
		Status:                inv.Status,
		StatusDetails:         inv.Status,
		ResponseCode:          inv.ResponseCode,
		StandardOutputContent: inv.Stdout,
		StandardErrorContent:  inv.Stderr,
	})
}

package ssm

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ssmnative "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

type ssmTarget struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

type sendCommandRequest struct {
	InstanceIds  []string            `json:"InstanceIds"`
	Targets      []ssmTarget         `json:"Targets"`
	DocumentName string              `json:"DocumentName"`
	Comment      string              `json:"Comment"`
	Parameters   map[string][]string `json:"Parameters"`
}

type commandJSON struct {
	CommandId    string      `json:"CommandId"`
	DocumentName string      `json:"DocumentName"`
	Status       string      `json:"Status"`
	InstanceIds  []string    `json:"InstanceIds"`
	Targets      []ssmTarget `json:"Targets,omitempty"`
	Comment      string      `json:"Comment,omitempty"`
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
func (h *Handler) runCommand() (ssmnative.RunCommand, bool) {
	rc, ok := h.store.(ssmnative.RunCommand)

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

	commandID, err := store.SendCommand(r.Context(), ssmnative.CommandConfig{
		InstanceIDs:  req.InstanceIds,
		Targets:      toDriverTargets(req.Targets),
		DocumentName: req.DocumentName,
		Comment:      req.Comment,
		Parameters:   req.Parameters,
	})
	if err != nil {
		// The provider names the exception: InvalidInstanceId for a target
		// that is not a managed instance, InvalidDocument for a document that
		// does not resolve.
		writeErr(w, err)

		return
	}

	// Real SSM reports the command as Pending here (it has been accepted, not
	// finished), and the caller learns the outcome from GetCommandInvocation.
	// Reporting Success would invite a caller to skip the poll it would need
	// against the real service.
	wire.WriteJSON(w, sendCommandResponse{Command: commandJSON{
		CommandId:    commandID,
		DocumentName: req.DocumentName,
		Status:       "Pending",
		InstanceIds:  req.InstanceIds,
		Targets:      req.Targets,
		Comment:      req.Comment,
	}})
}

// toDriverTargets converts wire Targets to the driver's CommandTarget shape.
func toDriverTargets(in []ssmTarget) []ssmnative.CommandTarget {
	if len(in) == 0 {
		return nil
	}

	out := make([]ssmnative.CommandTarget, 0, len(in))
	for _, t := range in {
		out = append(out, ssmnative.CommandTarget{Key: t.Key, Values: t.Values})
	}

	return out
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
		wire.WriteJSONError(w, http.StatusBadRequest, "InvocationDoesNotExist", cerrors.Message(err))

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

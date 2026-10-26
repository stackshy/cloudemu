package eventbridge

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

type replayNameRequest struct {
	ReplayName string `json:"ReplayName"`
}

type replayDestinationJSON struct {
	Arn        string   `json:"Arn"`
	FilterArns []string `json:"FilterArns,omitempty"`
}

type startReplayRequest struct {
	ReplayName     string                `json:"ReplayName"`
	Description    string                `json:"Description"`
	EventSourceArn string                `json:"EventSourceArn"`
	EventStartTime *float64              `json:"EventStartTime"`
	EventEndTime   *float64              `json:"EventEndTime"`
	Destination    replayDestinationJSON `json:"Destination"`
}

// replayStateResponse is the reply of StartReplay and CancelReplay.
type replayStateResponse struct {
	ReplayArn       string   `json:"ReplayArn"`
	State           string   `json:"State"`
	StateReason     string   `json:"StateReason,omitempty"`
	ReplayStartTime *float64 `json:"ReplayStartTime,omitempty"`
}

type replayJSON struct {
	ReplayName            string                 `json:"ReplayName"`
	ReplayArn             string                 `json:"ReplayArn,omitempty"`
	Description           string                 `json:"Description,omitempty"`
	State                 string                 `json:"State"`
	StateReason           string                 `json:"StateReason,omitempty"`
	EventSourceArn        string                 `json:"EventSourceArn"`
	Destination           *replayDestinationJSON `json:"Destination,omitempty"`
	EventStartTime        *float64               `json:"EventStartTime,omitempty"`
	EventEndTime          *float64               `json:"EventEndTime,omitempty"`
	EventLastReplayedTime *float64               `json:"EventLastReplayedTime,omitempty"`
	ReplayStartTime       *float64               `json:"ReplayStartTime,omitempty"`
	ReplayEndTime         *float64               `json:"ReplayEndTime,omitempty"`
}

type listReplaysResponse struct {
	Replays   []replayJSON `json:"Replays"`
	NextToken string       `json:"NextToken,omitempty"`
}

func toReplayJSON(rp *ebdriver.Replay, full bool) replayJSON {
	out := replayJSON{
		ReplayName:            rp.Name,
		State:                 rp.State,
		StateReason:           rp.StateReason,
		EventSourceArn:        rp.EventSourceARN,
		EventStartTime:        timeToEpoch(rp.EventStartTime),
		EventEndTime:          timeToEpoch(rp.EventEndTime),
		EventLastReplayedTime: timeToEpoch(rp.EventLastReplayedTime),
		ReplayStartTime:       timeToEpoch(rp.ReplayStartTime),
		ReplayEndTime:         timeToEpoch(rp.ReplayEndTime),
	}

	if full {
		out.ReplayArn = rp.ARN
		out.Description = rp.Description
		out.Destination = &replayDestinationJSON{Arn: rp.Destination.ARN, FilterArns: rp.Destination.FilterARNs}
	}

	return out
}

func startReplay(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req startReplayRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rp, err := mgr.StartReplay(r.Context(), &ebdriver.ReplayConfig{
		Name:           req.ReplayName,
		Description:    req.Description,
		EventSourceARN: req.EventSourceArn,
		EventStartTime: epochToTime(req.EventStartTime),
		EventEndTime:   epochToTime(req.EventEndTime),
		Destination:    ebdriver.ReplayDestination{ARN: req.Destination.Arn, FilterARNs: req.Destination.FilterArns},
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, replayStateResponse{
		ReplayArn: rp.ARN, State: rp.State, StateReason: rp.StateReason, ReplayStartTime: timeToEpoch(rp.ReplayStartTime),
	})
}

func describeReplay(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req replayNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rp, err := mgr.DescribeReplay(r.Context(), req.ReplayName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toReplayJSON(rp, true))
}

func cancelReplay(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req replayNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rp, err := mgr.CancelReplay(r.Context(), req.ReplayName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, replayStateResponse{ReplayArn: rp.ARN, State: rp.State, StateReason: rp.StateReason})
}

func fetchReplays(ctx context.Context, mgr archiveManager, req *listFilterRequest) ([]ebdriver.Replay, error) {
	return mgr.ListReplays(ctx, ebdriver.ReplayFilter{
		NamePrefix: req.NamePrefix, EventSourceARN: req.EventSourceArn, State: req.State,
	})
}

func replayListItem(rp *ebdriver.Replay) replayJSON { return toReplayJSON(rp, false) }

func replayPage(page []replayJSON, next string) any {
	return listReplaysResponse{Replays: page, NextToken: next}
}

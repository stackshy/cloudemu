package eventbridge

import (
	"context"
	"math"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// archiveManager is the AWS-specific EventBridge archive and replay surface,
// asserted against the provider (not part of the portable EventBus driver).
type archiveManager interface {
	CreateArchive(ctx context.Context, cfg *ebdriver.ArchiveConfig) (*ebdriver.Archive, error)
	DescribeArchive(ctx context.Context, name string) (*ebdriver.Archive, error)
	UpdateArchive(ctx context.Context, upd *ebdriver.ArchiveUpdate) (*ebdriver.Archive, error)
	DeleteArchive(ctx context.Context, name string) error
	ListArchives(ctx context.Context, filter ebdriver.ArchiveFilter) ([]ebdriver.Archive, error)
	StartReplay(ctx context.Context, cfg *ebdriver.ReplayConfig) (*ebdriver.Replay, error)
	DescribeReplay(ctx context.Context, name string) (*ebdriver.Replay, error)
	CancelReplay(ctx context.Context, name string) (*ebdriver.Replay, error)
	ListReplays(ctx context.Context, filter ebdriver.ReplayFilter) ([]ebdriver.Replay, error)
}

type archiveOp func(mgr archiveManager, w http.ResponseWriter, r *http.Request)

// archiveOps maps each archive and replay operation to its handler.
func archiveOps() map[string]archiveOp {
	return map[string]archiveOp{
		"CreateArchive":   createArchive,
		"DescribeArchive": describeArchive,
		"UpdateArchive":   updateArchive,
		"DeleteArchive":   deleteArchive,
		"ListArchives":    listOp(fetchArchives, archiveListItem, func(a archiveJSON) string { return a.ArchiveName }, archivePage),
		"StartReplay":     startReplay,
		"DescribeReplay":  describeReplay,
		"CancelReplay":    cancelReplay,
		"ListReplays":     listOp(fetchReplays, replayListItem, func(rp replayJSON) string { return rp.ReplayName }, replayPage),
	}
}

// serveArchiveOp serves op when it is an archive or replay operation, reporting
// whether it did.
func (h *Handler) serveArchiveOp(w http.ResponseWriter, r *http.Request, op string) bool {
	fn, ok := archiveOps()[op]
	if !ok {
		return false
	}

	mgr, ok := h.bus.(archiveManager)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "archives not supported"))
		return true
	}

	fn(mgr, w, r)

	return true
}

// epochToTime converts an AWS JSON timestamp (epoch seconds, possibly
// fractional) to a time.Time; nil gives the zero time.
func epochToTime(sec *float64) time.Time {
	if sec == nil {
		return time.Time{}
	}

	whole, frac := math.Modf(*sec)

	return time.Unix(int64(whole), int64(frac*float64(time.Second))).UTC()
}

// timeToEpoch converts a time to its AWS JSON wire form, omitting the zero time.
func timeToEpoch(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	sec := float64(t.UnixNano()) / float64(time.Second)

	return &sec
}

type archiveNameRequest struct {
	ArchiveName string `json:"ArchiveName"`
}

type createArchiveRequest struct {
	ArchiveName      string `json:"ArchiveName"`
	EventSourceArn   string `json:"EventSourceArn"`
	Description      string `json:"Description"`
	EventPattern     string `json:"EventPattern"`
	RetentionDays    int    `json:"RetentionDays"`
	KmsKeyIdentifier string `json:"KmsKeyIdentifier"`
}

type updateArchiveRequest struct {
	ArchiveName      string  `json:"ArchiveName"`
	Description      *string `json:"Description"`
	EventPattern     *string `json:"EventPattern"`
	RetentionDays    *int    `json:"RetentionDays"`
	KmsKeyIdentifier *string `json:"KmsKeyIdentifier"`
}

// archiveStateResponse is the reply of CreateArchive and UpdateArchive.
type archiveStateResponse struct {
	ArchiveArn   string   `json:"ArchiveArn"`
	State        string   `json:"State"`
	StateReason  string   `json:"StateReason,omitempty"`
	CreationTime *float64 `json:"CreationTime,omitempty"`
}

type archiveJSON struct {
	ArchiveArn       string   `json:"ArchiveArn,omitempty"`
	ArchiveName      string   `json:"ArchiveName"`
	EventSourceArn   string   `json:"EventSourceArn"`
	Description      string   `json:"Description,omitempty"`
	EventPattern     string   `json:"EventPattern,omitempty"`
	State            string   `json:"State"`
	StateReason      string   `json:"StateReason,omitempty"`
	KmsKeyIdentifier string   `json:"KmsKeyIdentifier,omitempty"`
	RetentionDays    int      `json:"RetentionDays"`
	SizeBytes        int64    `json:"SizeBytes"`
	EventCount       int64    `json:"EventCount"`
	CreationTime     *float64 `json:"CreationTime,omitempty"`
}

type listArchivesResponse struct {
	Archives  []archiveJSON `json:"Archives"`
	NextToken string        `json:"NextToken,omitempty"`
}

func toArchiveState(a *ebdriver.Archive) archiveStateResponse {
	return archiveStateResponse{
		ArchiveArn: a.ARN, State: a.State, StateReason: a.StateReason, CreationTime: timeToEpoch(a.CreatedAt),
	}
}

// listFilterRequest is the request shape ListArchives and ListReplays share.
type listFilterRequest struct {
	NamePrefix     string `json:"NamePrefix"`
	EventSourceArn string `json:"EventSourceArn"`
	State          string `json:"State"`
	Limit          int    `json:"Limit"`
	NextToken      string `json:"NextToken"`
}

// listOp builds the handler of a list operation: decode the shared filter
// request, fetch the items, convert them to their wire form, and write one page
// using EventBridge's cursor pagination.
func listOp[T, J any](
	fetch func(ctx context.Context, mgr archiveManager, req *listFilterRequest) ([]T, error),
	conv func(*T) J,
	key func(J) string,
	wrap func(page []J, next string) any,
) archiveOp {
	return func(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
		var req listFilterRequest
		if !wire.DecodeJSON(w, r, &req) {
			return
		}

		items, err := fetch(r.Context(), mgr, &req)
		if err != nil {
			writeErr(w, err)
			return
		}

		out := make([]J, 0, len(items))
		for i := range items {
			out = append(out, conv(&items[i]))
		}

		page, next := paginateByCursor(out, req.NextToken, req.Limit, key)

		wire.WriteJSON(w, wrap(page, next))
	}
}

func toArchiveJSON(a *ebdriver.Archive, full bool) archiveJSON {
	out := archiveJSON{
		ArchiveName:    a.Name,
		EventSourceArn: a.EventSourceARN,
		State:          a.State,
		StateReason:    a.StateReason,
		RetentionDays:  a.RetentionDays,
		SizeBytes:      a.SizeBytes,
		EventCount:     a.EventCount,
		CreationTime:   timeToEpoch(a.CreatedAt),
	}

	if full {
		out.ArchiveArn = a.ARN
		out.Description = a.Description
		out.EventPattern = a.EventPattern
		out.KmsKeyIdentifier = a.KMSKeyIdentifier
	}

	return out
}

func createArchive(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req createArchiveRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := mgr.CreateArchive(r.Context(), &ebdriver.ArchiveConfig{
		Name:             req.ArchiveName,
		EventSourceARN:   req.EventSourceArn,
		Description:      req.Description,
		EventPattern:     req.EventPattern,
		RetentionDays:    req.RetentionDays,
		KMSKeyIdentifier: req.KmsKeyIdentifier,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toArchiveState(a))
}

func describeArchive(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req archiveNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := mgr.DescribeArchive(r.Context(), req.ArchiveName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toArchiveJSON(a, true))
}

func updateArchive(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req updateArchiveRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	a, err := mgr.UpdateArchive(r.Context(), &ebdriver.ArchiveUpdate{
		Name:             req.ArchiveName,
		Description:      req.Description,
		EventPattern:     req.EventPattern,
		RetentionDays:    req.RetentionDays,
		KMSKeyIdentifier: req.KmsKeyIdentifier,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toArchiveState(a))
}

func deleteArchive(mgr archiveManager, w http.ResponseWriter, r *http.Request) {
	var req archiveNameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mgr.DeleteArchive(r.Context(), req.ArchiveName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func fetchArchives(ctx context.Context, mgr archiveManager, req *listFilterRequest) ([]ebdriver.Archive, error) {
	return mgr.ListArchives(ctx, ebdriver.ArchiveFilter{
		NamePrefix: req.NamePrefix, EventSourceARN: req.EventSourceArn, State: req.State,
	})
}

func archiveListItem(a *ebdriver.Archive) archiveJSON { return toArchiveJSON(a, false) }

func archivePage(page []archiveJSON, next string) any {
	return listArchivesResponse{Archives: page, NextToken: next}
}

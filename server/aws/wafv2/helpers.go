package wafv2

import (
	"context"
	"net/http"

	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// listOp collapses the identical decode/list/summarize/respond shape every
// List* operation shares. lister returns the driver resources for a scope,
// toSummary projects each to its wire summary, and resp wraps the summaries in
// the operation's response envelope.
func listOp[T any, R any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	lister func(context.Context, string) ([]T, error),
	toSummary func(*T) summaryJSON,
	resp func([]summaryJSON) R,
) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *listRequest) (any, error) {
		items, err := lister(ctx, req.Scope)
		if err != nil {
			return nil, err
		}

		out := make([]summaryJSON, 0, len(items))
		for i := range items {
			out = append(out, toSummary(&items[i]))
		}

		return resp(out), nil
	})
}

// deleteOp collapses the identical decode/delete/respond shape every Delete*
// operation shares.
func deleteOp(
	h *Handler, w http.ResponseWriter, r *http.Request,
	del func(context.Context, wafdriver.Ref, string) error,
) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *refRequest) (any, error) {
		if err := del(ctx, wafdriver.Ref{Scope: req.Scope, ID: req.ID}, req.LockToken); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// updateOp collapses the identical decode/update/respond shape every Update*
// operation shares: decode a request of type Req, invoke a driver update that
// returns the next lock token, and wrap it in the lock-token response envelope.
func updateOp[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	update func(context.Context, *Req) (string, error),
) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *Req) (any, error) {
		tok, err := update(ctx, req)
		if err != nil {
			return nil, err
		}

		return lockTokenResponse{NextLockToken: tok}, nil
	})
}

// getOp collapses the identical decode/get/project shape every Get* operation
// shares. getter fetches the driver resource; toResp projects it (and its lock
// token, read from the resource) to the operation's response envelope.
func getOp[T any, R any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	getter func(context.Context, wafdriver.Ref) (*T, error),
	toResp func(*T) R,
) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *refRequest) (any, error) {
		item, err := getter(ctx, wafdriver.Ref{Scope: req.Scope, ID: req.ID, Name: req.Name})
		if err != nil {
			return nil, err
		}

		return toResp(item), nil
	})
}

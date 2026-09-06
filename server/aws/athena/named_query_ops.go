package athena

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

type createNamedQueryRequest struct {
	Name               string `json:"Name"`
	Description        string `json:"Description"`
	Database           string `json:"Database"`
	QueryString        string `json:"QueryString"`
	ClientRequestToken string `json:"ClientRequestToken"`
	WorkGroup          string `json:"WorkGroup"`
}

type createNamedQueryResponse struct {
	NamedQueryID string `json:"NamedQueryId"`
}

func (h *Handler) createNamedQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createNamedQueryRequest) (any, error) {
		id, err := h.athena.CreateNamedQuery(ctx, driver.NamedQuery{
			Name:        req.Name,
			Description: req.Description,
			Database:    req.Database,
			QueryString: req.QueryString,
			WorkGroup:   req.WorkGroup,
		})
		if err != nil {
			return nil, err
		}

		return createNamedQueryResponse{NamedQueryID: id}, nil
	})
}

type getNamedQueryRequest struct {
	NamedQueryID string `json:"NamedQueryId"`
}

type getNamedQueryResponse struct {
	NamedQuery namedQueryJSON `json:"NamedQuery"`
}

func (h *Handler) getNamedQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getNamedQueryRequest) (any, error) {
		nq, err := h.athena.GetNamedQuery(ctx, req.NamedQueryID)
		if err != nil {
			return nil, err
		}

		return getNamedQueryResponse{NamedQuery: namedQueryToWire(nq)}, nil
	})
}

func (h *Handler) deleteNamedQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getNamedQueryRequest) (any, error) {
		if err := h.athena.DeleteNamedQuery(ctx, req.NamedQueryID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listNamedQueriesRequest struct {
	WorkGroup  string `json:"WorkGroup"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listNamedQueriesResponse struct {
	NamedQueryIDs []string `json:"NamedQueryIds"`
	NextToken     string   `json:"NextToken,omitempty"`
}

func (h *Handler) listNamedQueries(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listNamedQueriesRequest) (any, error) {
		ids, next, err := h.athena.ListNamedQueries(ctx, req.WorkGroup,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return listNamedQueriesResponse{NamedQueryIDs: ids, NextToken: next}, nil
	})
}

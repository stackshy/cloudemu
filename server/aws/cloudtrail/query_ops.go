package cloudtrail

import (
	"context"
	"net/http"
)

func (h *Handler) startQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		QueryStatement      string `json:"QueryStatement"`
		DeliveryS3Uri       string `json:"DeliveryS3Uri"`
		QueryAlias          string `json:"QueryAlias"`
		EventDataStoreOwner string `json:"EventDataStoreOwnerAccountId"`
	},
	) (any, error) {
		id, err := h.ct.StartQuery(ctx, "", "", req.DeliveryS3Uri, req.QueryStatement)
		if err != nil {
			return nil, err
		}

		return struct {
			QueryID string `json:"QueryId"`
		}{QueryID: id}, nil
	})
}

func (h *Handler) describeQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore string `json:"EventDataStore"`
		QueryID        string `json:"QueryId"`
		QueryAlias     string `json:"QueryAlias"`
	},
	) (any, error) {
		q, err := h.ct.DescribeQuery(ctx, req.EventDataStore, req.QueryID, req.QueryAlias)
		if err != nil {
			return nil, err
		}

		return struct {
			QueryID     string `json:"QueryId,omitempty"`
			QueryString string `json:"QueryString,omitempty"`
			QueryStatus string `json:"QueryStatus,omitempty"`
		}{QueryID: q.ID, QueryString: q.QueryString, QueryStatus: q.Status}, nil
	})
}

func (h *Handler) getQueryResults(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore  string `json:"EventDataStore"`
		QueryID         string `json:"QueryId"`
		NextToken       string `json:"NextToken"`
		MaxQueryResults int32  `json:"MaxQueryResults"`
	},
	) (any, error) {
		res, err := h.ct.GetQueryResults(ctx, req.EventDataStore, req.QueryID, req.NextToken, req.MaxQueryResults)
		if err != nil {
			return nil, err
		}

		return struct {
			QueryStatus     string              `json:"QueryStatus,omitempty"`
			QueryResultRows []map[string]string `json:"QueryResultRows"`
			NextToken       string              `json:"NextToken,omitempty"`
		}{QueryStatus: res.QueryStatus, QueryResultRows: res.ResultRows, NextToken: res.NextToken}, nil
	})
}

func (h *Handler) cancelQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore string `json:"EventDataStore"`
		QueryID        string `json:"QueryId"`
	},
	) (any, error) {
		status, err := h.ct.CancelQuery(ctx, req.EventDataStore, req.QueryID)
		if err != nil {
			return nil, err
		}

		return struct {
			QueryID     string `json:"QueryId"`
			QueryStatus string `json:"QueryStatus"`
		}{QueryID: req.QueryID, QueryStatus: status}, nil
	})
}

func (h *Handler) listQueries(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStore string `json:"EventDataStore"`
		NextToken      string `json:"NextToken"`
		MaxResults     int32  `json:"MaxResults"`
	},
	) (any, error) {
		queries, next, err := h.ct.ListQueries(ctx, req.EventDataStore, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		type item struct {
			QueryID     string `json:"QueryId,omitempty"`
			QueryStatus string `json:"QueryStatus,omitempty"`
		}

		list := make([]item, 0, len(queries))
		for i := range queries {
			list = append(list, item{QueryID: queries[i].ID, QueryStatus: queries[i].Status})
		}

		return struct {
			Queries   []item `json:"Queries"`
			NextToken string `json:"NextToken,omitempty"`
		}{Queries: list, NextToken: next}, nil
	})
}

func (h *Handler) generateQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		EventDataStores []string `json:"EventDataStores"`
		Prompt          string   `json:"Prompt"`
	},
	) (any, error) {
		alias, stmt, err := h.ct.GenerateQuery(ctx, req.EventDataStores, req.Prompt)
		if err != nil {
			return nil, err
		}

		return struct {
			QueryAlias     string `json:"QueryAlias,omitempty"`
			QueryStatement string `json:"QueryStatement,omitempty"`
		}{QueryAlias: alias, QueryStatement: stmt}, nil
	})
}

package athena

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

type startQueryExecutionRequest struct {
	QueryString           string                     `json:"QueryString"`
	ClientRequestToken    string                     `json:"ClientRequestToken"`
	QueryExecutionContext *queryExecutionContextJSON `json:"QueryExecutionContext"`
	ResultConfiguration   *resultConfigJSON          `json:"ResultConfiguration"`
	WorkGroup             string                     `json:"WorkGroup"`
}

type startQueryExecutionResponse struct {
	QueryExecutionID string `json:"QueryExecutionId"`
}

func (h *Handler) startQueryExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startQueryExecutionRequest) (any, error) {
		in := driver.StartQueryExecutionInput{
			QueryString:         req.QueryString,
			ClientRequestToken:  req.ClientRequestToken,
			ResultConfiguration: resultConfigFromWire(req.ResultConfiguration),
			WorkGroup:           req.WorkGroup,
		}
		if req.QueryExecutionContext != nil {
			in.QueryExecutionContext = &driver.QueryExecutionContext{
				Database: req.QueryExecutionContext.Database,
				Catalog:  req.QueryExecutionContext.Catalog,
			}
		}

		id, err := h.athena.StartQueryExecution(ctx, in)
		if err != nil {
			return nil, err
		}

		return startQueryExecutionResponse{QueryExecutionID: id}, nil
	})
}

type getQueryExecutionRequest struct {
	QueryExecutionID string `json:"QueryExecutionId"`
}

type getQueryExecutionResponse struct {
	QueryExecution queryExecutionJSON `json:"QueryExecution"`
}

func (h *Handler) getQueryExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getQueryExecutionRequest) (any, error) {
		qe, err := h.athena.GetQueryExecution(ctx, req.QueryExecutionID)
		if err != nil {
			return nil, err
		}

		return getQueryExecutionResponse{QueryExecution: queryExecutionToWire(qe)}, nil
	})
}

type getQueryResultsRequest struct {
	QueryExecutionID string `json:"QueryExecutionId"`
	NextToken        string `json:"NextToken"`
	MaxResults       int32  `json:"MaxResults"`
}

type resultSetMetadataJSON struct {
	ColumnInfo []columnInfoJSON `json:"ColumnInfo"`
}

type columnInfoJSON struct {
	Name string `json:"Name,omitempty"`
	Type string `json:"Type,omitempty"`
}

type datumJSON struct {
	VarCharValue string `json:"VarCharValue,omitempty"`
}

type rowJSON struct {
	Data []datumJSON `json:"Data"`
}

type resultSetJSON struct {
	Rows              []rowJSON             `json:"Rows"`
	ResultSetMetadata resultSetMetadataJSON `json:"ResultSetMetadata"`
}

type getQueryResultsResponse struct {
	ResultSet   resultSetJSON `json:"ResultSet"`
	UpdateCount int64         `json:"UpdateCount,omitempty"`
	NextToken   string        `json:"NextToken,omitempty"`
}

func (h *Handler) getQueryResults(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getQueryResultsRequest) (any, error) {
		res, err := h.athena.GetQueryResults(ctx, req.QueryExecutionID,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return queryResultsToWire(res), nil
	})
}

// queryResultsToWire builds the GetQueryResults response, always emitting
// non-null Rows and ColumnInfo arrays so SDK decoders see empty slices.
func queryResultsToWire(res *driver.QueryResults) getQueryResultsResponse {
	cols := make([]columnInfoJSON, 0, len(res.ColumnInfo))
	for _, c := range res.ColumnInfo {
		cols = append(cols, columnInfoJSON{Name: c.Name, Type: c.Type})
	}

	rows := make([]rowJSON, 0, len(res.Rows))

	for _, row := range res.Rows {
		rows = append(rows, rowToWire(row))
	}

	return getQueryResultsResponse{
		ResultSet:   resultSetJSON{Rows: rows, ResultSetMetadata: resultSetMetadataJSON{ColumnInfo: cols}},
		UpdateCount: res.UpdateCount,
		NextToken:   res.NextToken,
	}
}

// rowToWire converts one driver result row to its wire shape.
func rowToWire(row driver.Row) rowJSON {
	data := make([]datumJSON, 0, len(row.Data))
	for _, d := range row.Data {
		data = append(data, datumJSON{VarCharValue: d.VarCharValue})
	}

	return rowJSON{Data: data}
}

func (h *Handler) stopQueryExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getQueryExecutionRequest) (any, error) {
		if err := h.athena.StopQueryExecution(ctx, req.QueryExecutionID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listQueryExecutionsRequest struct {
	WorkGroup  string `json:"WorkGroup"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listQueryExecutionsResponse struct {
	QueryExecutionIDs []string `json:"QueryExecutionIds"`
	NextToken         string   `json:"NextToken,omitempty"`
}

func (h *Handler) listQueryExecutions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listQueryExecutionsRequest) (any, error) {
		ids, next, err := h.athena.ListQueryExecutions(ctx, req.WorkGroup,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return listQueryExecutionsResponse{QueryExecutionIDs: ids, NextToken: next}, nil
	})
}

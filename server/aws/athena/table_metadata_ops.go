package athena

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

type columnJSON struct {
	Name    string `json:"Name"`
	Type    string `json:"Type,omitempty"`
	Comment string `json:"Comment,omitempty"`
}

type tableMetadataJSON struct {
	Name           string            `json:"Name"`
	CreateTime     *float64          `json:"CreateTime,omitempty"`
	LastAccessTime *float64          `json:"LastAccessTime,omitempty"`
	TableType      string            `json:"TableType,omitempty"`
	Columns        []columnJSON      `json:"Columns,omitempty"`
	PartitionKeys  []columnJSON      `json:"PartitionKeys,omitempty"`
	Parameters     map[string]string `json:"Parameters,omitempty"`
}

type getTableMetadataRequest struct {
	CatalogName  string `json:"CatalogName"`
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
}

type getTableMetadataResponse struct {
	TableMetadata tableMetadataJSON `json:"TableMetadata"`
}

func (h *Handler) getTableMetadata(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTableMetadataRequest) (any, error) {
		tm, err := h.athena.GetTableMetadata(ctx, req.CatalogName, req.DatabaseName, req.TableName)
		if err != nil {
			return nil, err
		}

		return getTableMetadataResponse{TableMetadata: tableMetadataToWire(tm)}, nil
	})
}

type listTableMetadataRequest struct {
	CatalogName  string `json:"CatalogName"`
	DatabaseName string `json:"DatabaseName"`
	Expression   string `json:"Expression"`
	NextToken    string `json:"NextToken"`
	MaxResults   int32  `json:"MaxResults"`
}

type listTableMetadataResponse struct {
	TableMetadataList []tableMetadataJSON `json:"TableMetadataList"`
	NextToken         string              `json:"NextToken,omitempty"`
}

func (h *Handler) listTableMetadata(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTableMetadataRequest) (any, error) {
		tables, next, err := h.athena.ListTableMetadata(ctx, req.CatalogName, req.DatabaseName, req.Expression,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]tableMetadataJSON, 0, len(tables))
		for i := range tables {
			out = append(out, tableMetadataToWire(&tables[i]))
		}

		return listTableMetadataResponse{TableMetadataList: out, NextToken: next}, nil
	})
}

func tableMetadataToWire(tm *driver.TableMetadata) tableMetadataJSON {
	return tableMetadataJSON{
		Name:           tm.Name,
		CreateTime:     epochOrNil(tm.CreateTime),
		LastAccessTime: epochOrNil(tm.LastAccessTime),
		TableType:      tm.TableType,
		Columns:        columnsToWire(tm.Columns),
		PartitionKeys:  columnsToWire(tm.PartitionKeys),
		Parameters:     tm.Parameters,
	}
}

func columnsToWire(in []driver.Column) []columnJSON {
	if len(in) == 0 {
		return nil
	}

	out := make([]columnJSON, len(in))
	for i, c := range in {
		out[i] = columnJSON{Name: c.Name, Type: c.Type, Comment: c.Comment}
	}

	return out
}

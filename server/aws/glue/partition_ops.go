package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type createPartitionRequest struct {
	CatalogID      string             `json:"CatalogId"`
	DatabaseName   string             `json:"DatabaseName"`
	TableName      string             `json:"TableName"`
	PartitionInput partitionInputJSON `json:"PartitionInput"`
}

func (h *Handler) createPartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createPartitionRequest) (any, error) {
		p := partFromInput(req.CatalogID, req.DatabaseName, req.TableName, req.PartitionInput)
		if err := h.glue.CreatePartition(ctx, req.CatalogID, req.DatabaseName, req.TableName, p); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getPartitionRequest struct {
	CatalogID       string   `json:"CatalogId"`
	DatabaseName    string   `json:"DatabaseName"`
	TableName       string   `json:"TableName"`
	PartitionValues []string `json:"PartitionValues"`
}

type getPartitionResponse struct {
	Partition partitionJSON `json:"Partition"`
}

func (h *Handler) getPartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getPartitionRequest) (any, error) {
		p, err := h.glue.GetPartition(ctx, req.CatalogID, req.DatabaseName, req.TableName, req.PartitionValues)
		if err != nil {
			return nil, err
		}

		return getPartitionResponse{Partition: partToWire(p)}, nil
	})
}

type updatePartitionRequest struct {
	CatalogID          string             `json:"CatalogId"`
	DatabaseName       string             `json:"DatabaseName"`
	TableName          string             `json:"TableName"`
	PartitionValueList []string           `json:"PartitionValueList"`
	PartitionInput     partitionInputJSON `json:"PartitionInput"`
}

func (h *Handler) updatePartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updatePartitionRequest) (any, error) {
		p := partFromInput(req.CatalogID, req.DatabaseName, req.TableName, req.PartitionInput)
		if err := h.glue.UpdatePartition(ctx, req.CatalogID, req.DatabaseName, req.TableName,
			req.PartitionValueList, p); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deletePartitionRequest struct {
	CatalogID       string   `json:"CatalogId"`
	DatabaseName    string   `json:"DatabaseName"`
	TableName       string   `json:"TableName"`
	PartitionValues []string `json:"PartitionValues"`
}

func (h *Handler) deletePartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deletePartitionRequest) (any, error) {
		if err := h.glue.DeletePartition(ctx, req.CatalogID, req.DatabaseName,
			req.TableName, req.PartitionValues); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getPartitionsRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
	NextToken    string `json:"NextToken"`
	MaxResults   int32  `json:"MaxResults"`
}

type getPartitionsResponse struct {
	Partitions []partitionJSON `json:"Partitions"`
	NextToken  string          `json:"NextToken,omitempty"`
}

func (h *Handler) getPartitions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getPartitionsRequest) (any, error) {
		ps, next, err := h.glue.GetPartitions(ctx, req.CatalogID, req.DatabaseName, req.TableName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return getPartitionsResponse{Partitions: partsToWire(ps), NextToken: next}, nil
	})
}

type batchCreatePartitionRequest struct {
	CatalogID          string               `json:"CatalogId"`
	DatabaseName       string               `json:"DatabaseName"`
	TableName          string               `json:"TableName"`
	PartitionInputList []partitionInputJSON `json:"PartitionInputList"`
}

type partitionErrorJSON struct {
	PartitionValues []string         `json:"PartitionValues,omitempty"`
	ErrorDetail     *errorDetailJSON `json:"ErrorDetail,omitempty"`
}

type partitionErrorsResponse struct {
	Errors []partitionErrorJSON `json:"Errors,omitempty"`
}

func partErrorsToWire(errs []driver.BatchError) []partitionErrorJSON {
	out := make([]partitionErrorJSON, 0, len(errs))
	for i := range errs {
		out = append(out, partitionErrorJSON{
			PartitionValues: errs[i].Values,
			ErrorDetail:     &errorDetailJSON{ErrorCode: errs[i].ErrorCode, ErrorMessage: errs[i].ErrorMessage},
		})
	}

	return out
}

func (h *Handler) batchCreatePartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchCreatePartitionRequest) (any, error) {
		ps := make([]driver.Partition, 0, len(req.PartitionInputList))
		for i := range req.PartitionInputList {
			ps = append(ps, partFromInput(req.CatalogID, req.DatabaseName, req.TableName, req.PartitionInputList[i]))
		}

		errs, err := h.glue.BatchCreatePartition(ctx, req.CatalogID, req.DatabaseName, req.TableName, ps)
		if err != nil {
			return nil, err
		}

		return partitionErrorsResponse{Errors: partErrorsToWire(errs)}, nil
	})
}

type partitionValueListJSON struct {
	Values []string `json:"Values"`
}

type batchDeletePartitionRequest struct {
	CatalogID          string                   `json:"CatalogId"`
	DatabaseName       string                   `json:"DatabaseName"`
	TableName          string                   `json:"TableName"`
	PartitionsToDelete []partitionValueListJSON `json:"PartitionsToDelete"`
}

func (h *Handler) batchDeletePartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchDeletePartitionRequest) (any, error) {
		values := make([][]string, 0, len(req.PartitionsToDelete))
		for i := range req.PartitionsToDelete {
			values = append(values, req.PartitionsToDelete[i].Values)
		}

		errs, err := h.glue.BatchDeletePartition(ctx, req.CatalogID, req.DatabaseName, req.TableName, values)
		if err != nil {
			return nil, err
		}

		return partitionErrorsResponse{Errors: partErrorsToWire(errs)}, nil
	})
}

type batchUpdatePartitionEntryJSON struct {
	PartitionValueList []string           `json:"PartitionValueList"`
	PartitionInput     partitionInputJSON `json:"PartitionInput"`
}

type batchUpdatePartitionRequest struct {
	CatalogID    string                          `json:"CatalogId"`
	DatabaseName string                          `json:"DatabaseName"`
	TableName    string                          `json:"TableName"`
	Entries      []batchUpdatePartitionEntryJSON `json:"Entries"`
}

type batchUpdatePartitionFailureJSON struct {
	PartitionValueList []string         `json:"PartitionValueList,omitempty"`
	ErrorDetail        *errorDetailJSON `json:"ErrorDetail,omitempty"`
}

type batchUpdatePartitionResponse struct {
	Errors []batchUpdatePartitionFailureJSON `json:"Errors,omitempty"`
}

func (h *Handler) batchUpdatePartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchUpdatePartitionRequest) (any, error) {
		entries := make([]driver.BatchUpdatePartitionEntry, 0, len(req.Entries))
		for i := range req.Entries {
			entries = append(entries, driver.BatchUpdatePartitionEntry{
				PartitionValueList: req.Entries[i].PartitionValueList,
				Partition:          partFromInput(req.CatalogID, req.DatabaseName, req.TableName, req.Entries[i].PartitionInput),
			})
		}

		errs, err := h.glue.BatchUpdatePartition(ctx, req.CatalogID, req.DatabaseName, req.TableName, entries)
		if err != nil {
			return nil, err
		}

		out := make([]batchUpdatePartitionFailureJSON, 0, len(errs))
		for i := range errs {
			out = append(out, batchUpdatePartitionFailureJSON{
				PartitionValueList: errs[i].Values,
				ErrorDetail:        &errorDetailJSON{ErrorCode: errs[i].ErrorCode, ErrorMessage: errs[i].ErrorMessage},
			})
		}

		return batchUpdatePartitionResponse{Errors: out}, nil
	})
}

type batchGetPartitionRequest struct {
	CatalogID       string                   `json:"CatalogId"`
	DatabaseName    string                   `json:"DatabaseName"`
	TableName       string                   `json:"TableName"`
	PartitionsToGet []partitionValueListJSON `json:"PartitionsToGet"`
}

type batchGetPartitionResponse struct {
	Partitions      []partitionJSON          `json:"Partitions"`
	UnprocessedKeys []partitionValueListJSON `json:"UnprocessedKeys,omitempty"`
}

func (h *Handler) batchGetPartition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetPartitionRequest) (any, error) {
		values := make([][]string, 0, len(req.PartitionsToGet))
		for i := range req.PartitionsToGet {
			values = append(values, req.PartitionsToGet[i].Values)
		}

		found, unprocessed, err := h.glue.BatchGetPartition(ctx, req.CatalogID, req.DatabaseName,
			req.TableName, values)
		if err != nil {
			return nil, err
		}

		up := make([]partitionValueListJSON, 0, len(unprocessed))
		for i := range unprocessed {
			up = append(up, partitionValueListJSON{Values: unprocessed[i]})
		}

		return batchGetPartitionResponse{Partitions: partsToWire(found), UnprocessedKeys: up}, nil
	})
}

package aoss

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// registerCollectionRoutes wires the collection operations.
func (h *Handler) registerCollectionRoutes() {
	h.routes["CreateCollection"] = h.createCollection
	h.routes["BatchGetCollection"] = h.batchGetCollection
	h.routes["ListCollections"] = h.listCollections
	h.routes["UpdateCollection"] = h.updateCollection
	h.routes["DeleteCollection"] = h.deleteCollection
}

type createCollectionRequest struct {
	ClientToken     string    `json:"clientToken"`
	Description     string    `json:"description"`
	Name            string    `json:"name"`
	StandbyReplicas string    `json:"standbyReplicas"`
	Tags            []tagJSON `json:"tags"`
	Type            string    `json:"type"`
}

type createCollectionResponse struct {
	CreateCollectionDetail createCollectionDetailJSON `json:"createCollectionDetail"`
}

func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createCollectionRequest) (any, error) {
		col, err := h.aoss.CreateCollection(ctx, &driver.CreateCollectionInput{
			Name:            req.Name,
			Description:     req.Description,
			Type:            req.Type,
			StandbyReplicas: req.StandbyReplicas,
			Tags:            tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createCollectionResponse{CreateCollectionDetail: toCreateCollectionDetail(col)}, nil
	})
}

type batchGetCollectionRequest struct {
	IDs   []string `json:"ids"`
	Names []string `json:"names"`
}

type batchGetCollectionResponse struct {
	CollectionDetails      []collectionDetailJSON `json:"collectionDetails"`
	CollectionErrorDetails []collectionErrorJSON  `json:"collectionErrorDetails"`
}

func (h *Handler) batchGetCollection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetCollectionRequest) (any, error) {
		details, errs, err := h.aoss.BatchGetCollection(ctx, req.IDs, req.Names)
		if err != nil {
			return nil, err
		}

		resp := batchGetCollectionResponse{
			CollectionDetails:      make([]collectionDetailJSON, 0, len(details)),
			CollectionErrorDetails: make([]collectionErrorJSON, 0, len(errs)),
		}
		for i := range details {
			resp.CollectionDetails = append(resp.CollectionDetails, toCollectionDetail(&details[i]))
		}

		for _, e := range errs {
			resp.CollectionErrorDetails = append(resp.CollectionErrorDetails, collectionErrorJSON{
				ErrorCode: e.ErrorCode, ErrorMessage: e.ErrorMessage, ID: e.ID, Name: e.Name,
			})
		}

		return resp, nil
	})
}

type collectionFiltersJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type listCollectionsRequest struct {
	CollectionFilters collectionFiltersJSON `json:"collectionFilters"`
	MaxResults        int32                 `json:"maxResults"`
	NextToken         string                `json:"nextToken"`
}

type listCollectionsResponse struct {
	CollectionSummaries []collectionSummaryJSON `json:"collectionSummaries"`
	NextToken           string                  `json:"nextToken,omitempty"`
}

func (h *Handler) listCollections(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listCollectionsRequest) (any, error) {
		cols, next, err := h.aoss.ListCollections(ctx, req.CollectionFilters.Name, req.CollectionFilters.Status,
			driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		resp := listCollectionsResponse{
			CollectionSummaries: make([]collectionSummaryJSON, 0, len(cols)),
			NextToken:           next,
		}
		for i := range cols {
			resp.CollectionSummaries = append(resp.CollectionSummaries, collectionSummaryJSON{
				ARN: cols[i].ARN, ID: cols[i].ID, Name: cols[i].Name, Status: cols[i].Status,
			})
		}

		return resp, nil
	})
}

type updateCollectionRequest struct {
	ClientToken string  `json:"clientToken"`
	Description *string `json:"description"`
	ID          string  `json:"id"`
}

type updateCollectionResponse struct {
	UpdateCollectionDetail updateCollectionDetailJSON `json:"updateCollectionDetail"`
}

func (h *Handler) updateCollection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateCollectionRequest) (any, error) {
		col, err := h.aoss.UpdateCollection(ctx, &driver.UpdateCollectionInput{
			ID:          req.ID,
			Description: req.Description,
		})
		if err != nil {
			return nil, err
		}

		return updateCollectionResponse{UpdateCollectionDetail: toUpdateCollectionDetail(col)}, nil
	})
}

type deleteCollectionRequest struct {
	ClientToken string `json:"clientToken"`
	ID          string `json:"id"`
}

type deleteCollectionDetailJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type deleteCollectionResponse struct {
	DeleteCollectionDetail deleteCollectionDetailJSON `json:"deleteCollectionDetail"`
}

func (h *Handler) deleteCollection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteCollectionRequest) (any, error) {
		col, err := h.aoss.DeleteCollection(ctx, req.ID)
		if err != nil {
			return nil, err
		}

		return deleteCollectionResponse{DeleteCollectionDetail: deleteCollectionDetailJSON{
			ID: col.ID, Name: col.Name, Status: col.Status,
		}}, nil
	})
}

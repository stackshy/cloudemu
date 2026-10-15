package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerFeaturedRoutes wires the featured results operations.
//
//nolint:dupl // the list handlers of the index children share one shape
func (h *Handler) registerFeaturedRoutes(d driver.FeaturedResults) {
	h.routes["CreateFeaturedResultsSet"] = handle(h, func(ctx context.Context, req *createFeaturedRequest) (featuredResponse, error) {
		f, err := d.CreateFeaturedResultsSet(ctx, &driver.CreateFeaturedResultsSetInput{
			IndexID: req.IndexID, Name: req.FeaturedResultsSetName, Description: req.Description, Status: req.Status,
			QueryTexts: req.QueryTexts, FeaturedDocuments: docsToIDs(req.FeaturedDocuments),
			ClientToken: req.ClientToken, Tags: tagsFromWire(req.Tags),
		})
		if err != nil {
			return featuredResponse{}, err
		}

		return featuredResponse{FeaturedResultsSet: featuredToWire(f)}, nil
	})
	h.routes["DescribeFeaturedResultsSet"] = handle(h,
		func(ctx context.Context, req *describeFeaturedRequest) (describeFeaturedResponse, error) {
			v, err := d.DescribeFeaturedResultsSet(ctx, req.IndexID, req.FeaturedResultsSetID)
			if err != nil {
				return describeFeaturedResponse{}, err
			}

			return describeFeaturedToWire(v), nil
		})
	h.routes["UpdateFeaturedResultsSet"] = handle(h, func(ctx context.Context, req *updateFeaturedRequest) (featuredResponse, error) {
		f, err := d.UpdateFeaturedResultsSet(ctx, req.toInput())
		if err != nil {
			return featuredResponse{}, err
		}

		return featuredResponse{FeaturedResultsSet: featuredToWire(f)}, nil
	})
	h.routes["ListFeaturedResultsSets"] = handle(h, func(ctx context.Context, req *listChildrenRequest) (listFeaturedResponse, error) {
		items, next, err := d.ListFeaturedResultsSets(ctx, req.IndexID, req.page())
		if err != nil {
			return listFeaturedResponse{}, err
		}

		out := listFeaturedResponse{FeaturedResultsSetSummaryItems: make([]featuredSummaryJSON, len(items)), NextToken: next}

		for i := range items {
			f := &items[i]
			out.FeaturedResultsSetSummaryItems[i] = featuredSummaryJSON{
				FeaturedResultsSetID: f.ID, FeaturedResultsSetName: f.Name, Status: f.Status,
				CreationTimestamp: epochSeconds(f.CreatedAt), LastUpdatedTimestamp: epochSeconds(f.UpdatedAt),
			}
		}

		return out, nil
	})
	h.routes["BatchDeleteFeaturedResultsSet"] = handle(h,
		func(ctx context.Context, req *batchDeleteFeaturedRequest) (batchDeleteFeaturedResponse, error) {
			errs, err := d.BatchDeleteFeaturedResultsSet(ctx, req.IndexID, req.FeaturedResultsSetIDs)
			if err != nil {
				return batchDeleteFeaturedResponse{}, err
			}

			out := batchDeleteFeaturedResponse{Errors: make([]batchDeleteFeaturedErrorJSON, len(errs))}
			for i, e := range errs {
				out.Errors[i] = batchDeleteFeaturedErrorJSON(e)
			}

			return out, nil
		})
}

type featuredDocumentJSON struct {
	ID string `json:"Id"`
}

func docsToIDs(in []featuredDocumentJSON) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].ID
	}

	return out
}

func idsToDocs(in []string) []featuredDocumentJSON {
	out := make([]featuredDocumentJSON, len(in))
	for i := range in {
		out[i] = featuredDocumentJSON{ID: in[i]}
	}

	return out
}

type featuredSetJSON struct {
	FeaturedResultsSetID   string                 `json:"FeaturedResultsSetId"`
	FeaturedResultsSetName string                 `json:"FeaturedResultsSetName"`
	Description            string                 `json:"Description,omitempty"`
	Status                 string                 `json:"Status"`
	QueryTexts             []string               `json:"QueryTexts"`
	FeaturedDocuments      []featuredDocumentJSON `json:"FeaturedDocuments"`
	CreationTimestamp      int64                  `json:"CreationTimestamp"`
	LastUpdatedTimestamp   int64                  `json:"LastUpdatedTimestamp"`
}

func featuredToWire(f *driver.FeaturedResultsSet) featuredSetJSON {
	return featuredSetJSON{
		FeaturedResultsSetID: f.ID, FeaturedResultsSetName: f.Name, Description: f.Description, Status: f.Status,
		QueryTexts: nonNil(f.QueryTexts), FeaturedDocuments: idsToDocs(f.FeaturedDocuments),
		CreationTimestamp: epochSeconds(f.CreatedAt), LastUpdatedTimestamp: epochSeconds(f.UpdatedAt),
	}
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

type featuredResponse struct {
	FeaturedResultsSet featuredSetJSON `json:"FeaturedResultsSet"`
}

type createFeaturedRequest struct {
	IndexID                string                 `json:"IndexId"`
	FeaturedResultsSetName string                 `json:"FeaturedResultsSetName"`
	Description            string                 `json:"Description"`
	Status                 string                 `json:"Status"`
	QueryTexts             []string               `json:"QueryTexts"`
	FeaturedDocuments      []featuredDocumentJSON `json:"FeaturedDocuments"`
	ClientToken            string                 `json:"ClientToken"`
	Tags                   []tagJSON              `json:"Tags"`
}

func describeFeaturedToWire(v *driver.FeaturedResultsSetView) describeFeaturedResponse {
	out := describeFeaturedResponse{
		FeaturedResultsSetID: v.ID, FeaturedResultsSetName: v.Name, Description: v.Description, Status: v.Status,
		QueryTexts:                    nonNil(v.QueryTexts),
		FeaturedDocumentsWithMetadata: make([]featuredDocumentMetadataJSON, len(v.DocumentsWithMetadata)),
		FeaturedDocumentsMissing:      idsToDocs(v.DocumentsMissing),
		CreationTimestamp:             epochSeconds(v.CreatedAt), LastUpdatedTimestamp: epochSeconds(v.UpdatedAt),
	}

	for i, doc := range v.DocumentsWithMetadata {
		out.FeaturedDocumentsWithMetadata[i] = featuredDocumentMetadataJSON(doc)
	}

	return out
}

type listFeaturedResponse struct {
	FeaturedResultsSetSummaryItems []featuredSummaryJSON `json:"FeaturedResultsSetSummaryItems"`
	NextToken                      string                `json:"NextToken,omitempty"`
}

type batchDeleteFeaturedResponse struct {
	Errors []batchDeleteFeaturedErrorJSON `json:"Errors"`
}

type describeFeaturedRequest struct {
	IndexID              string `json:"IndexId"`
	FeaturedResultsSetID string `json:"FeaturedResultsSetId"`
}

type featuredDocumentMetadataJSON struct {
	ID    string `json:"Id"`
	Title string `json:"Title,omitempty"`
	URI   string `json:"URI,omitempty"`
}

type describeFeaturedResponse struct {
	FeaturedResultsSetID          string                         `json:"FeaturedResultsSetId"`
	FeaturedResultsSetName        string                         `json:"FeaturedResultsSetName"`
	Description                   string                         `json:"Description,omitempty"`
	Status                        string                         `json:"Status"`
	QueryTexts                    []string                       `json:"QueryTexts"`
	FeaturedDocumentsWithMetadata []featuredDocumentMetadataJSON `json:"FeaturedDocumentsWithMetadata"`
	FeaturedDocumentsMissing      []featuredDocumentJSON         `json:"FeaturedDocumentsMissing"`
	CreationTimestamp             int64                          `json:"CreationTimestamp"`
	LastUpdatedTimestamp          int64                          `json:"LastUpdatedTimestamp"`
}

type updateFeaturedRequest struct {
	IndexID                string                  `json:"IndexId"`
	FeaturedResultsSetID   string                  `json:"FeaturedResultsSetId"`
	FeaturedResultsSetName *string                 `json:"FeaturedResultsSetName"`
	Description            *string                 `json:"Description"`
	Status                 *string                 `json:"Status"`
	QueryTexts             *[]string               `json:"QueryTexts"`
	FeaturedDocuments      *[]featuredDocumentJSON `json:"FeaturedDocuments"`
}

type featuredSummaryJSON struct {
	FeaturedResultsSetID   string `json:"FeaturedResultsSetId"`
	FeaturedResultsSetName string `json:"FeaturedResultsSetName"`
	Status                 string `json:"Status"`
	CreationTimestamp      int64  `json:"CreationTimestamp"`
	LastUpdatedTimestamp   int64  `json:"LastUpdatedTimestamp"`
}

type batchDeleteFeaturedRequest struct {
	IndexID               string   `json:"IndexId"`
	FeaturedResultsSetIDs []string `json:"FeaturedResultsSetIds"`
}

type batchDeleteFeaturedErrorJSON struct {
	ID           string `json:"Id"`
	ErrorCode    string `json:"ErrorCode"`
	ErrorMessage string `json:"ErrorMessage"`
}

func (r *updateFeaturedRequest) toInput() *driver.UpdateFeaturedResultsSetInput {
	in := &driver.UpdateFeaturedResultsSetInput{
		IndexID: r.IndexID, ID: r.FeaturedResultsSetID, Name: r.FeaturedResultsSetName,
		Description: r.Description, Status: r.Status,
	}

	if r.QueryTexts != nil {
		in.QueryTexts, in.QueryTextsSet = *r.QueryTexts, true
	}

	if r.FeaturedDocuments != nil {
		in.FeaturedDocuments, in.DocumentsSet = docsToIDs(*r.FeaturedDocuments), true
	}

	return in
}

package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerThesaurusRoutes wires the thesaurus operations.
func (h *Handler) registerThesaurusRoutes(d driver.Thesauri) {
	h.routes["CreateThesaurus"] = handle(h, func(ctx context.Context, req *createSourceFileRequest) (idResponse, error) {
		t, err := d.CreateThesaurus(ctx, &driver.CreateThesaurusInput{
			IndexID: req.IndexID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			SourceS3Path: s3FromWire(req.SourceS3Path), ClientToken: req.ClientToken, Tags: tagsFromWire(req.Tags),
		})
		if err != nil {
			return idResponse{}, err
		}

		return idResponse{ID: t.ID}, nil
	})
	h.routes["DescribeThesaurus"] = handle(h, func(ctx context.Context, req *childRef) (describeSourceFileResponse, error) {
		t, err := d.DescribeThesaurus(ctx, req.IndexID, req.ID)
		if err != nil {
			return describeSourceFileResponse{}, err
		}

		out := sourceFileToWire(&t.SourceFile)
		out.TermCount, out.SynonymRuleCount = &t.TermCount, &t.SynonymRuleCount

		return out, nil
	})
	h.routes["UpdateThesaurus"] = handle(h, func(ctx context.Context, req *updateSourceFileRequest) (struct{}, error) {
		return ack(d.UpdateThesaurus(ctx, &driver.UpdateThesaurusInput{
			IndexID: req.IndexID, ID: req.ID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			SourceS3Path: s3PathFromWire(req.SourceS3Path),
		}))
	})
	h.routes["ListThesauri"] = handle(h, func(ctx context.Context, req *listChildrenRequest) (listThesauriResponse, error) {
		items, next, err := d.ListThesauri(ctx, req.IndexID, req.page())
		if err != nil {
			return listThesauriResponse{}, err
		}

		out := listThesauriResponse{ThesaurusSummaryItems: make([]sourceFileSummaryJSON, len(items)), NextToken: next}
		for i := range items {
			out.ThesaurusSummaryItems[i] = sourceFileSummary(&items[i].SourceFile)
		}

		return out, nil
	})
	h.routes["DeleteThesaurus"] = handle(h, func(ctx context.Context, req *childRef) (struct{}, error) {
		return ack(d.DeleteThesaurus(ctx, req.IndexID, req.ID))
	})
}

// registerBlockListRoutes wires the query suggestions block list operations.
func (h *Handler) registerBlockListRoutes(d driver.BlockLists) {
	h.routes["CreateQuerySuggestionsBlockList"] = handle(h, func(ctx context.Context, req *createSourceFileRequest) (idResponse, error) {
		b, err := d.CreateQuerySuggestionsBlockList(ctx, &driver.CreateBlockListInput{
			IndexID: req.IndexID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			SourceS3Path: s3FromWire(req.SourceS3Path), ClientToken: req.ClientToken, Tags: tagsFromWire(req.Tags),
		})
		if err != nil {
			return idResponse{}, err
		}

		return idResponse{ID: b.ID}, nil
	})
	h.routes["DescribeQuerySuggestionsBlockList"] = handle(h,
		func(ctx context.Context, req *childRef) (describeSourceFileResponse, error) {
			b, err := d.DescribeQuerySuggestionsBlockList(ctx, req.IndexID, req.ID)
			if err != nil {
				return describeSourceFileResponse{}, err
			}

			out := sourceFileToWire(&b.SourceFile)
			out.ItemCount = &b.ItemCount

			return out, nil
		})
	h.routes["UpdateQuerySuggestionsBlockList"] = handle(h, func(ctx context.Context, req *updateSourceFileRequest) (struct{}, error) {
		return ack(d.UpdateQuerySuggestionsBlockList(ctx, &driver.UpdateBlockListInput{
			IndexID: req.IndexID, ID: req.ID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			SourceS3Path: s3PathFromWire(req.SourceS3Path),
		}))
	})
	h.routes["ListQuerySuggestionsBlockLists"] = handle(h,
		func(ctx context.Context, req *listChildrenRequest) (listBlockListsResponse, error) {
			items, next, err := d.ListQuerySuggestionsBlockLists(ctx, req.IndexID, req.page())
			if err != nil {
				return listBlockListsResponse{}, err
			}

			out := listBlockListsResponse{BlockListSummaryItems: make([]sourceFileSummaryJSON, len(items)), NextToken: next}

			for i := range items {
				s := sourceFileSummary(&items[i].SourceFile)
				s.ItemCount = &items[i].ItemCount
				out.BlockListSummaryItems[i] = s
			}

			return out, nil
		})
	h.routes["DeleteQuerySuggestionsBlockList"] = handle(h, func(ctx context.Context, req *childRef) (struct{}, error) {
		return ack(d.DeleteQuerySuggestionsBlockList(ctx, req.IndexID, req.ID))
	})
}

type createSourceFileRequest struct {
	IndexID      string      `json:"IndexId"`
	Name         string      `json:"Name"`
	Description  string      `json:"Description"`
	RoleArn      string      `json:"RoleArn"`
	SourceS3Path *s3PathJSON `json:"SourceS3Path"`
	ClientToken  string      `json:"ClientToken"`
	Tags         []tagJSON   `json:"Tags"`
}

type updateSourceFileRequest struct {
	ID           string      `json:"Id"`
	IndexID      string      `json:"IndexId"`
	Name         *string     `json:"Name"`
	Description  *string     `json:"Description"`
	RoleArn      *string     `json:"RoleArn"`
	SourceS3Path *s3PathJSON `json:"SourceS3Path"`
}

// describeSourceFileResponse is the Describe body of a thesaurus or block list;
// the count members that belong to only one of them are omitted for the other.
type describeSourceFileResponse struct {
	ID               string      `json:"Id"`
	IndexID          string      `json:"IndexId"`
	Name             string      `json:"Name"`
	Description      string      `json:"Description,omitempty"`
	RoleArn          string      `json:"RoleArn,omitempty"`
	SourceS3Path     *s3PathJSON `json:"SourceS3Path"`
	Status           string      `json:"Status"`
	ErrorMessage     string      `json:"ErrorMessage,omitempty"`
	FileSizeBytes    int64       `json:"FileSizeBytes"`
	TermCount        *int64      `json:"TermCount,omitempty"`
	SynonymRuleCount *int64      `json:"SynonymRuleCount,omitempty"`
	ItemCount        *int32      `json:"ItemCount,omitempty"`
	CreatedAt        int64       `json:"CreatedAt"`
	UpdatedAt        int64       `json:"UpdatedAt"`
}

func sourceFileToWire(f *driver.SourceFile) describeSourceFileResponse {
	return describeSourceFileResponse{
		ID: f.ID, IndexID: f.IndexID, Name: f.Name, Description: f.Description, RoleArn: f.RoleArn,
		SourceS3Path: s3ToWire(f.SourceS3Path), Status: f.Status, ErrorMessage: f.ErrorMessage,
		FileSizeBytes: f.FileSizeBytes, CreatedAt: epochSeconds(f.CreatedAt), UpdatedAt: epochSeconds(f.UpdatedAt),
	}
}

type sourceFileSummaryJSON struct {
	ID        string `json:"Id"`
	Name      string `json:"Name"`
	Status    string `json:"Status"`
	ItemCount *int32 `json:"ItemCount,omitempty"`
	CreatedAt int64  `json:"CreatedAt"`
	UpdatedAt int64  `json:"UpdatedAt"`
}

func sourceFileSummary(f *driver.SourceFile) sourceFileSummaryJSON {
	return sourceFileSummaryJSON{
		ID: f.ID, Name: f.Name, Status: f.Status, CreatedAt: epochSeconds(f.CreatedAt), UpdatedAt: epochSeconds(f.UpdatedAt),
	}
}

type listThesauriResponse struct {
	ThesaurusSummaryItems []sourceFileSummaryJSON `json:"ThesaurusSummaryItems"`
	NextToken             string                  `json:"NextToken,omitempty"`
}

type listBlockListsResponse struct {
	BlockListSummaryItems []sourceFileSummaryJSON `json:"BlockListSummaryItems"`
	NextToken             string                  `json:"NextToken,omitempty"`
}

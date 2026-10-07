package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerFaqRoutes wires the FAQ operations.
func (h *Handler) registerFaqRoutes(d driver.Faqs) {
	h.routes["CreateFaq"] = handle(h, func(ctx context.Context, req *createFaqRequest) (idResponse, error) {
		f, err := d.CreateFaq(ctx, &driver.CreateFaqInput{
			IndexID: req.IndexID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			S3Path: s3FromWire(req.S3Path), FileFormat: req.FileFormat, LanguageCode: req.LanguageCode,
			ClientToken: req.ClientToken, Tags: tagsFromWire(req.Tags),
		})
		if err != nil {
			return idResponse{}, err
		}

		return idResponse{ID: f.ID}, nil
	})
	h.routes["DescribeFaq"] = handle(h, func(ctx context.Context, req *childRef) (describeFaqResponse, error) {
		f, err := d.DescribeFaq(ctx, req.IndexID, req.ID)
		if err != nil {
			return describeFaqResponse{}, err
		}

		return describeFaqResponse{
			ID: f.ID, IndexID: f.IndexID, Name: f.Name, Description: f.Description, RoleArn: f.RoleArn,
			S3Path: s3ToWire(f.S3Path), FileFormat: f.FileFormat, LanguageCode: f.LanguageCode, Status: f.Status,
			ErrorMessage: f.ErrorMessage, CreatedAt: epochSeconds(f.CreatedAt), UpdatedAt: epochSeconds(f.UpdatedAt),
		}, nil
	})
	h.routes["ListFaqs"] = handle(h, func(ctx context.Context, req *listChildrenRequest) (listFaqsResponse, error) {
		faqs, next, err := d.ListFaqs(ctx, req.IndexID, req.page())
		if err != nil {
			return listFaqsResponse{}, err
		}

		out := listFaqsResponse{FaqSummaryItems: make([]faqSummaryJSON, len(faqs)), NextToken: next}

		for i := range faqs {
			f := &faqs[i]
			out.FaqSummaryItems[i] = faqSummaryJSON{
				ID: f.ID, Name: f.Name, FileFormat: f.FileFormat, LanguageCode: f.LanguageCode, Status: f.Status,
				CreatedAt: epochSeconds(f.CreatedAt), UpdatedAt: epochSeconds(f.UpdatedAt),
			}
		}

		return out, nil
	})
	h.routes["DeleteFaq"] = handle(h, func(ctx context.Context, req *childRef) (struct{}, error) {
		return ack(d.DeleteFaq(ctx, req.IndexID, req.ID))
	})
}

func s3FromWire(p *s3PathJSON) driver.S3Path {
	if p == nil {
		return driver.S3Path{}
	}

	return driver.S3Path{Bucket: p.Bucket, Key: p.Key}
}

func s3ToWire(p driver.S3Path) *s3PathJSON {
	return &s3PathJSON{Bucket: p.Bucket, Key: p.Key}
}

type createFaqRequest struct {
	IndexID      string      `json:"IndexId"`
	Name         string      `json:"Name"`
	Description  string      `json:"Description"`
	RoleArn      string      `json:"RoleArn"`
	S3Path       *s3PathJSON `json:"S3Path"`
	FileFormat   string      `json:"FileFormat"`
	LanguageCode string      `json:"LanguageCode"`
	ClientToken  string      `json:"ClientToken"`
	Tags         []tagJSON   `json:"Tags"`
}

// idResponse is the {"Id": ...} body the create calls return.
type idResponse struct {
	ID string `json:"Id"`
}

// childRef addresses one index child by its id.
type childRef struct {
	ID      string `json:"Id"`
	IndexID string `json:"IndexId"`
}

type describeFaqResponse struct {
	ID           string      `json:"Id"`
	IndexID      string      `json:"IndexId"`
	Name         string      `json:"Name"`
	Description  string      `json:"Description,omitempty"`
	RoleArn      string      `json:"RoleArn,omitempty"`
	S3Path       *s3PathJSON `json:"S3Path"`
	FileFormat   string      `json:"FileFormat"`
	LanguageCode string      `json:"LanguageCode,omitempty"`
	Status       string      `json:"Status"`
	ErrorMessage string      `json:"ErrorMessage,omitempty"`
	CreatedAt    int64       `json:"CreatedAt"`
	UpdatedAt    int64       `json:"UpdatedAt"`
}

// listChildrenRequest is the paging request shared by the index child lists.
type listChildrenRequest struct {
	IndexID    string `json:"IndexId"`
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

func (r *listChildrenRequest) page() driver.Page {
	return driver.Page{NextToken: r.NextToken, MaxResults: r.MaxResults}
}

type faqSummaryJSON struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	FileFormat   string `json:"FileFormat"`
	LanguageCode string `json:"LanguageCode,omitempty"`
	Status       string `json:"Status"`
	CreatedAt    int64  `json:"CreatedAt"`
	UpdatedAt    int64  `json:"UpdatedAt"`
}

type listFaqsResponse struct {
	FaqSummaryItems []faqSummaryJSON `json:"FaqSummaryItems"`
	NextToken       string           `json:"NextToken,omitempty"`
}

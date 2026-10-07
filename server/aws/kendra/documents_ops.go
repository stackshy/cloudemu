package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerDocumentRoutes wires the document ingestion and search operations.
func (h *Handler) registerDocumentRoutes(d driver.Documents) {
	h.routes["BatchPutDocument"] = handle(h, func(ctx context.Context, req *batchPutDocumentRequest) (failedDocumentsResponse, error) {
		failed, err := d.BatchPutDocument(ctx, req.toInput())
		if err != nil {
			return failedDocumentsResponse{}, err
		}

		return failedToWire(failed), nil
	})
	h.routes["BatchDeleteDocument"] = handle(h,
		func(ctx context.Context, req *batchDeleteDocumentRequest) (failedDocumentsResponse, error) {
			in := &driver.DeleteDocumentsInput{IndexID: req.IndexID, DocumentIDs: req.DocumentIDList}
			if t := req.DataSourceSyncJobMetricTarget; t != nil {
				in.MetricTarget = &driver.SyncJobMetricTarget{DataSourceID: t.DataSourceID, DataSourceSyncJobID: t.DataSourceSyncJobID}
			}

			failed, err := d.BatchDeleteDocument(ctx, in)
			if err != nil {
				return failedDocumentsResponse{}, err
			}

			return failedToWire(failed), nil
		})
	h.routes["BatchGetDocumentStatus"] = handle(h,
		func(ctx context.Context, req *batchGetDocumentStatusRequest) (batchGetDocumentStatusResponse, error) {
			infos := make([]driver.DocumentInfo, len(req.DocumentInfoList))
			for i := range req.DocumentInfoList {
				infos[i] = driver.DocumentInfo{
					DocumentID: req.DocumentInfoList[i].DocumentID, Attributes: attrsFromWire(req.DocumentInfoList[i].Attributes),
				}
			}

			res, err := d.BatchGetDocumentStatus(ctx, req.IndexID, infos)
			if err != nil {
				return batchGetDocumentStatusResponse{}, err
			}

			return documentStatusToWire(res), nil
		})
	h.routes["Query"] = handle(h, func(ctx context.Context, req *queryRequest) (queryResponse, error) {
		res, err := d.Query(ctx, req.toInput())
		if err != nil {
			return queryResponse{}, err
		}

		return queryToWire(res), nil
	})
	h.routes["Retrieve"] = handle(h, func(ctx context.Context, req *retrieveRequest) (retrieveResponse, error) {
		res, err := d.Retrieve(ctx, &driver.RetrieveInput{
			IndexID: req.IndexID, QueryText: req.QueryText, AttributeFilter: filterFromWire(req.AttributeFilter),
			PageNumber: req.PageNumber, PageSize: req.PageSize, RequestedAttributes: req.RequestedDocumentAttributes,
		})
		if err != nil {
			return retrieveResponse{}, err
		}

		return retrieveToWire(res), nil
	})
}

func documentStatusToWire(res *driver.DocumentStatusResult) batchGetDocumentStatusResponse {
	out := batchGetDocumentStatusResponse{
		DocumentStatusList: make([]documentStatusJSON, len(res.Statuses)),
		Errors:             make([]documentStatusErrorJSON, len(res.Errors)),
	}

	for i, s := range res.Statuses {
		out.DocumentStatusList[i] = documentStatusJSON{
			DocumentID: s.DocumentID, DocumentStatus: s.Status, FailureCode: s.FailureCode, FailureReason: s.FailureReason,
		}
	}

	for i, e := range res.Errors {
		out.Errors[i] = documentStatusErrorJSON{
			DocumentID: e.DocumentID, DataSourceID: e.DataSourceID, ErrorCode: e.ErrorCode, ErrorMessage: e.ErrorMessage,
		}
	}

	return out
}

func retrieveToWire(res *driver.RetrieveOutput) retrieveResponse {
	out := retrieveResponse{QueryID: res.QueryID, ResultItems: make([]retrieveResultItemJSON, len(res.Items))}

	for i := range res.Items {
		it := &res.Items[i]
		out.ResultItems[i] = retrieveResultItemJSON{
			ID: it.ID, DocumentID: it.DocumentID, DocumentTitle: it.Title, DocumentURI: it.URI, Content: it.Content,
			DocumentAttributes: attrsToWire(it.Attributes), ScoreAttributes: scoreAttributesJSON{ScoreConfidence: it.Score},
		}
	}

	return out
}

type s3PathJSON struct {
	Bucket string `json:"Bucket"`
	Key    string `json:"Key"`
}

func s3PathFromWire(p *s3PathJSON) *driver.S3Path {
	if p == nil {
		return nil
	}

	return &driver.S3Path{Bucket: p.Bucket, Key: p.Key}
}

type putDocumentJSON struct {
	ID                            string        `json:"Id"`
	Title                         string        `json:"Title"`
	Blob                          []byte        `json:"Blob"`
	S3Path                        *s3PathJSON   `json:"S3Path"`
	ContentType                   string        `json:"ContentType"`
	Attributes                    []docAttrJSON `json:"Attributes"`
	AccessControlConfigurationID  string        `json:"AccessControlConfigurationId"`
	AccessControlList             rawJSON       `json:"AccessControlList"`
	HierarchicalAccessControlList rawJSON       `json:"HierarchicalAccessControlList"`
}

type batchPutDocumentRequest struct {
	IndexID                               string            `json:"IndexId"`
	RoleArn                               string            `json:"RoleArn"`
	Documents                             []putDocumentJSON `json:"Documents"`
	CustomDocumentEnrichmentConfiguration rawJSON           `json:"CustomDocumentEnrichmentConfiguration"`
}

type failedDocumentJSON struct {
	ID           string `json:"Id"`
	DataSourceID string `json:"DataSourceId,omitempty"`
	ErrorCode    string `json:"ErrorCode"`
	ErrorMessage string `json:"ErrorMessage"`
}

type failedDocumentsResponse struct {
	FailedDocuments []failedDocumentJSON `json:"FailedDocuments"`
}

func failedToWire(in []driver.FailedDocument) failedDocumentsResponse {
	out := failedDocumentsResponse{FailedDocuments: make([]failedDocumentJSON, len(in))}
	for i := range in {
		out.FailedDocuments[i] = failedDocumentJSON{
			ID: in[i].ID, DataSourceID: in[i].DataSourceID, ErrorCode: in[i].ErrorCode, ErrorMessage: in[i].ErrorMessage,
		}
	}

	return out
}

type batchDeleteDocumentRequest struct {
	IndexID                       string   `json:"IndexId"`
	DocumentIDList                []string `json:"DocumentIdList"`
	DataSourceSyncJobMetricTarget *struct {
		DataSourceID        string `json:"DataSourceId"`
		DataSourceSyncJobID string `json:"DataSourceSyncJobId"`
	} `json:"DataSourceSyncJobMetricTarget"`
}

type batchGetDocumentStatusRequest struct {
	IndexID          string `json:"IndexId"`
	DocumentInfoList []struct {
		DocumentID string        `json:"DocumentId"`
		Attributes []docAttrJSON `json:"Attributes"`
	} `json:"DocumentInfoList"`
}

type documentStatusJSON struct {
	DocumentID     string `json:"DocumentId"`
	DocumentStatus string `json:"DocumentStatus"`
	FailureCode    string `json:"FailureCode,omitempty"`
	FailureReason  string `json:"FailureReason,omitempty"`
}

type documentStatusErrorJSON struct {
	DocumentID   string `json:"DocumentId"`
	DataSourceID string `json:"DataSourceId,omitempty"`
	ErrorCode    string `json:"ErrorCode"`
	ErrorMessage string `json:"ErrorMessage"`
}

type batchGetDocumentStatusResponse struct {
	DocumentStatusList []documentStatusJSON      `json:"DocumentStatusList"`
	Errors             []documentStatusErrorJSON `json:"Errors"`
}

type facetJSON struct {
	DocumentAttributeKey string `json:"DocumentAttributeKey"`
	MaxResults           int32  `json:"MaxResults"`
}

type sortingJSON struct {
	DocumentAttributeKey string `json:"DocumentAttributeKey"`
	SortOrder            string `json:"SortOrder"`
}

type queryRequest struct {
	IndexID                     string               `json:"IndexId"`
	QueryText                   string               `json:"QueryText"`
	AttributeFilter             *attributeFilterJSON `json:"AttributeFilter"`
	Facets                      []facetJSON          `json:"Facets"`
	PageNumber                  int32                `json:"PageNumber"`
	PageSize                    int32                `json:"PageSize"`
	RequestedDocumentAttributes []string             `json:"RequestedDocumentAttributes"`
	QueryResultTypeFilter       string               `json:"QueryResultTypeFilter"`
	SortingConfiguration        *sortingJSON         `json:"SortingConfiguration"`
	SortingConfigurations       []sortingJSON        `json:"SortingConfigurations"`
}

type highlightJSON struct {
	BeginOffset int32  `json:"BeginOffset"`
	EndOffset   int32  `json:"EndOffset"`
	TopAnswer   bool   `json:"TopAnswer"`
	Type        string `json:"Type,omitempty"`
}

type textWithHighlightsJSON struct {
	Text       string          `json:"Text"`
	Highlights []highlightJSON `json:"Highlights"`
}

func textToWire(t driver.TextWithHighlights) textWithHighlightsJSON {
	out := textWithHighlightsJSON{Text: t.Text, Highlights: make([]highlightJSON, len(t.Highlights))}
	for i, h := range t.Highlights {
		out.Highlights[i] = highlightJSON{BeginOffset: h.BeginOffset, EndOffset: h.EndOffset, TopAnswer: h.TopAnswer, Type: h.Type}
	}

	return out
}

type scoreAttributesJSON struct {
	ScoreConfidence string `json:"ScoreConfidence"`
}

type queryResultItemJSON struct {
	ID                 string                 `json:"Id"`
	Type               string                 `json:"Type"`
	Format             string                 `json:"Format"`
	DocumentID         string                 `json:"DocumentId"`
	DocumentTitle      textWithHighlightsJSON `json:"DocumentTitle"`
	DocumentExcerpt    textWithHighlightsJSON `json:"DocumentExcerpt"`
	DocumentURI        string                 `json:"DocumentURI,omitempty"`
	DocumentAttributes []docAttrJSON          `json:"DocumentAttributes"`
	ScoreAttributes    scoreAttributesJSON    `json:"ScoreAttributes"`
}

type facetValueCountJSON struct {
	DocumentAttributeValue docAttrValueJSON `json:"DocumentAttributeValue"`
	Count                  int32            `json:"Count"`
}

type facetResultJSON struct {
	DocumentAttributeKey             string                `json:"DocumentAttributeKey"`
	DocumentAttributeValueType       string                `json:"DocumentAttributeValueType"`
	DocumentAttributeValueCountPairs []facetValueCountJSON `json:"DocumentAttributeValueCountPairs"`
}

type queryResponse struct {
	QueryID              string                `json:"QueryId"`
	TotalNumberOfResults int32                 `json:"TotalNumberOfResults"`
	ResultItems          []queryResultItemJSON `json:"ResultItems"`
	FacetResults         []facetResultJSON     `json:"FacetResults"`
}

func queryToWire(res *driver.QueryOutput) queryResponse {
	out := queryResponse{
		QueryID: res.QueryID, TotalNumberOfResults: res.Total,
		ResultItems:  make([]queryResultItemJSON, len(res.Items)),
		FacetResults: make([]facetResultJSON, len(res.Facets)),
	}

	for i := range res.Items {
		it := &res.Items[i]
		out.ResultItems[i] = queryResultItemJSON{
			ID: it.ID, Type: it.Type, Format: it.Format, DocumentID: it.DocumentID,
			DocumentTitle: textToWire(it.Title), DocumentExcerpt: textToWire(it.Excerpt), DocumentURI: it.URI,
			DocumentAttributes: attrsToWire(it.Attributes), ScoreAttributes: scoreAttributesJSON{ScoreConfidence: it.Score},
		}
	}

	for i := range res.Facets {
		f := &res.Facets[i]
		fr := facetResultJSON{
			DocumentAttributeKey: f.Key, DocumentAttributeValueType: f.ValueType,
			DocumentAttributeValueCountPairs: make([]facetValueCountJSON, len(f.Counts)),
		}

		for j, c := range f.Counts {
			fr.DocumentAttributeValueCountPairs[j] = facetValueCountJSON{DocumentAttributeValue: valueToWire(c.Value), Count: c.Count}
		}

		out.FacetResults[i] = fr
	}

	return out
}

type retrieveRequest struct {
	IndexID                     string               `json:"IndexId"`
	QueryText                   string               `json:"QueryText"`
	AttributeFilter             *attributeFilterJSON `json:"AttributeFilter"`
	PageNumber                  int32                `json:"PageNumber"`
	PageSize                    int32                `json:"PageSize"`
	RequestedDocumentAttributes []string             `json:"RequestedDocumentAttributes"`
}

type retrieveResultItemJSON struct {
	ID                 string              `json:"Id"`
	DocumentID         string              `json:"DocumentId"`
	DocumentTitle      string              `json:"DocumentTitle"`
	DocumentURI        string              `json:"DocumentURI,omitempty"`
	Content            string              `json:"Content"`
	DocumentAttributes []docAttrJSON       `json:"DocumentAttributes"`
	ScoreAttributes    scoreAttributesJSON `json:"ScoreAttributes"`
}

type retrieveResponse struct {
	QueryID     string                   `json:"QueryId"`
	ResultItems []retrieveResultItemJSON `json:"ResultItems"`
}

func (r *batchPutDocumentRequest) toInput() *driver.PutDocumentsInput {
	docs := make([]driver.PutDocument, len(r.Documents))

	for i := range r.Documents {
		p := &r.Documents[i]
		docs[i] = driver.PutDocument{
			ID: p.ID, Title: p.Title, Blob: p.Blob, S3Path: s3PathFromWire(p.S3Path), ContentType: p.ContentType,
			Attributes: attrsFromWire(p.Attributes), AccessControlConfigurationID: p.AccessControlConfigurationID,
			AccessControlList: p.AccessControlList, HierarchicalAccessControlList: p.HierarchicalAccessControlList,
		}
	}

	return &driver.PutDocumentsInput{
		IndexID: r.IndexID, RoleArn: r.RoleArn, Documents: docs,
		CustomDocumentEnrichmentConfiguration: r.CustomDocumentEnrichmentConfiguration,
	}
}

func (r *queryRequest) toInput() *driver.QueryInput {
	in := &driver.QueryInput{
		IndexID: r.IndexID, QueryText: r.QueryText, AttributeFilter: filterFromWire(r.AttributeFilter),
		PageNumber: r.PageNumber, PageSize: r.PageSize, RequestedAttributes: r.RequestedDocumentAttributes,
		ResultTypeFilter: r.QueryResultTypeFilter,
	}

	for _, f := range r.Facets {
		in.Facets = append(in.Facets, driver.Facet{DocumentAttributeKey: f.DocumentAttributeKey, MaxResults: f.MaxResults})
	}

	if r.SortingConfiguration != nil {
		in.Sorting = append(in.Sorting, driver.SortingConfig(*r.SortingConfiguration))
	}

	for _, s := range r.SortingConfigurations {
		in.Sorting = append(in.Sorting, driver.SortingConfig(s))
	}

	return in
}

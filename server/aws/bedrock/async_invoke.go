package bedrock

import (
	"encoding/json"
	"net/http"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- wire types ---

type asyncS3OutputConfig struct {
	S3URI       string `json:"s3Uri"`
	BucketOwner string `json:"bucketOwner,omitempty"`
	KMSKeyID    string `json:"kmsKeyId,omitempty"`
}

type asyncOutputDataConfig struct {
	S3OutputDataConfig *asyncS3OutputConfig `json:"s3OutputDataConfig,omitempty"`
}

type startAsyncInvokeRequest struct {
	ClientRequestToken string                 `json:"clientRequestToken"`
	ModelID            string                 `json:"modelId"`
	ModelInput         json.RawMessage        `json:"modelInput"`
	OutputDataConfig   *asyncOutputDataConfig `json:"outputDataConfig"`
	Tags               []tagPair              `json:"tags"`
}

type startAsyncInvokeResponse struct {
	InvocationARN string `json:"invocationArn"`
}

type asyncInvokeJSON struct {
	InvocationARN      string                 `json:"invocationArn"`
	ModelARN           string                 `json:"modelArn"`
	Status             string                 `json:"status"`
	SubmitTime         string                 `json:"submitTime,omitempty"`
	OutputDataConfig   *asyncOutputDataConfig `json:"outputDataConfig,omitempty"`
	ClientRequestToken string                 `json:"clientRequestToken,omitempty"`
	EndTime            string                 `json:"endTime,omitempty"`
	LastModifiedTime   string                 `json:"lastModifiedTime,omitempty"`
	FailureMessage     string                 `json:"failureMessage,omitempty"`
}

// asyncInvokeSummaryJSON mirrors the SDK's AsyncInvokeSummary shape.
type asyncInvokeSummaryJSON struct {
	InvocationARN      string                 `json:"invocationArn"`
	ModelARN           string                 `json:"modelArn"`
	Status             string                 `json:"status"`
	SubmitTime         string                 `json:"submitTime,omitempty"`
	OutputDataConfig   *asyncOutputDataConfig `json:"outputDataConfig,omitempty"`
	ClientRequestToken string                 `json:"clientRequestToken,omitempty"`
	EndTime            string                 `json:"endTime,omitempty"`
	LastModifiedTime   string                 `json:"lastModifiedTime,omitempty"`
	FailureMessage     string                 `json:"failureMessage,omitempty"`
}

type listAsyncInvokesResponse struct {
	AsyncInvokeSummaries []asyncInvokeSummaryJSON `json:"asyncInvokeSummaries"`
	NextToken            string                   `json:"nextToken,omitempty"`
}

// --- dispatchers ---

// serveAsyncJobs routes the async-invoke and control-plane job surfaces. Split
// out of ServeHTTP to keep each dispatcher small.
func (h *Handler) serveAsyncJobs(w http.ResponseWriter, r *http.Request, p string) {
	switch {
	case p == prefixAsyncInvoke || strings.HasPrefix(p, prefixAsyncInvoke+"/"):
		h.serveAsyncInvoke(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixAsyncInvoke), "/"))
	case p == prefixImportJobs || strings.HasPrefix(p, prefixImportJobs+"/"):
		h.serveImportJobs(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixImportJobs), "/"))
	case p == prefixCopyJobs || strings.HasPrefix(p, prefixCopyJobs+"/"):
		h.serveCopyJobs(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixCopyJobs), "/"))
	case p == prefixEvalJobs || strings.HasPrefix(p, prefixEvalJobs+"/"):
		h.serveEvalJobs(w, r, strings.TrimPrefix(strings.TrimPrefix(p, prefixEvalJobs), "/"))
	case strings.HasPrefix(p, prefixEvalJobStop):
		h.serveEvalJobStop(w, r, strings.TrimPrefix(p, prefixEvalJobStop))
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported path: "+p)
	}
}

// serveAsyncInvoke handles /async-invoke[/{invocationArn}]. The invocation ARN
// contains slashes, so it is the entire remainder of the path.
func (h *Handler) serveAsyncInvoke(w http.ResponseWriter, r *http.Request, arn string) {
	if arn == "" {
		switch r.Method {
		case http.MethodPost:
			h.startAsyncInvoke(w, r)
		case http.MethodGet:
			h.listAsyncInvokes(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.getAsyncInvoke(w, r, arn)
}

// --- operations ---

func (h *Handler) startAsyncInvoke(w http.ResponseWriter, r *http.Request) {
	var in startAsyncInvokeRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	inv, err := h.bedrock.StartAsyncInvoke(r.Context(), bedrockdriver.StartAsyncInvokeConfig{
		ClientRequestToken: in.ClientRequestToken,
		ModelID:            in.ModelID,
		ModelInput:         []byte(in.ModelInput),
		Output:             toDriverAsyncOutput(in.OutputDataConfig),
		Tags:               tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, startAsyncInvokeResponse{InvocationARN: inv.InvocationARN})
}

func (h *Handler) getAsyncInvoke(w http.ResponseWriter, r *http.Request, arn string) {
	inv, err := h.bedrock.GetAsyncInvoke(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toAsyncInvokeJSON(inv))
}

func (h *Handler) listAsyncInvokes(w http.ResponseWriter, r *http.Request) {
	invs, err := h.bedrock.ListAsyncInvokes(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]asyncInvokeSummaryJSON, 0, len(invs))
	for i := range invs {
		out = append(out, toAsyncInvokeSummaryJSON(&invs[i]))
	}

	writeJSON(w, listAsyncInvokesResponse{AsyncInvokeSummaries: out})
}

// --- converters ---

func toDriverAsyncOutput(in *asyncOutputDataConfig) bedrockdriver.AsyncInvokeOutputConfig {
	if in == nil || in.S3OutputDataConfig == nil {
		return bedrockdriver.AsyncInvokeOutputConfig{}
	}

	s3 := in.S3OutputDataConfig

	return bedrockdriver.AsyncInvokeOutputConfig{S3URI: s3.S3URI, BucketOwner: s3.BucketOwner, KMSKeyID: s3.KMSKeyID}
}

func toAsyncOutputJSON(o bedrockdriver.AsyncInvokeOutputConfig) *asyncOutputDataConfig {
	if o.S3URI == "" {
		return nil
	}

	return &asyncOutputDataConfig{
		S3OutputDataConfig: &asyncS3OutputConfig{S3URI: o.S3URI, BucketOwner: o.BucketOwner, KMSKeyID: o.KMSKeyID},
	}
}

func toAsyncInvokeJSON(inv *bedrockdriver.AsyncInvoke) asyncInvokeJSON {
	return asyncInvokeJSON{
		InvocationARN:      inv.InvocationARN,
		ModelARN:           inv.ModelARN,
		Status:             inv.Status,
		SubmitTime:         inv.SubmitTime,
		OutputDataConfig:   toAsyncOutputJSON(inv.Output),
		ClientRequestToken: inv.ClientRequestToken,
		EndTime:            inv.EndTime,
		LastModifiedTime:   inv.LastModifiedTime,
		FailureMessage:     inv.FailureMessage,
	}
}

func toAsyncInvokeSummaryJSON(inv *bedrockdriver.AsyncInvoke) asyncInvokeSummaryJSON {
	return asyncInvokeSummaryJSON{
		InvocationARN:      inv.InvocationARN,
		ModelARN:           inv.ModelARN,
		Status:             inv.Status,
		SubmitTime:         inv.SubmitTime,
		OutputDataConfig:   toAsyncOutputJSON(inv.Output),
		ClientRequestToken: inv.ClientRequestToken,
		EndTime:            inv.EndTime,
		LastModifiedTime:   inv.LastModifiedTime,
		FailureMessage:     inv.FailureMessage,
	}
}

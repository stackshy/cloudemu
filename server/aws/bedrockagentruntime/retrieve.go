package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type retrievalConfigurationJSON struct {
	VectorSearchConfiguration *struct {
		NumberOfResults *int32          `json:"numberOfResults"`
		Filter          json.RawMessage `json:"filter"`
	} `json:"vectorSearchConfiguration"`
}

type retrieveRequest struct {
	RetrievalQuery struct {
		Text string `json:"text"`
	} `json:"retrievalQuery"`
	NextToken              string                      `json:"nextToken"`
	RetrievalConfiguration *retrievalConfigurationJSON `json:"retrievalConfiguration"`
	GuardrailConfiguration *guardrailRequestJSON       `json:"guardrailConfiguration"`
}

// guardrailRequestJSON is the GuardrailConfiguration of Retrieve and
// RetrieveAndGenerate (guardrailId, not guardrailIdentifier).
type guardrailRequestJSON struct {
	GuardrailID      string `json:"guardrailId"`
	GuardrailVersion string `json:"guardrailVersion"`
}

func (g *guardrailRequestJSON) toDriver() *driver.GuardrailRef {
	if g == nil || g.GuardrailID == "" {
		return nil
	}

	return &driver.GuardrailRef{ID: g.GuardrailID, Version: g.GuardrailVersion}
}

func (c *retrievalConfigurationJSON) numberOfResults() *int32 {
	if c == nil || c.VectorSearchConfiguration == nil {
		return nil
	}

	return c.VectorSearchConfiguration.NumberOfResults
}

func (c *retrievalConfigurationJSON) filter() json.RawMessage {
	if c == nil || c.VectorSearchConfiguration == nil {
		return nil
	}

	return c.VectorSearchConfiguration.Filter
}

type retrievalResultJSON struct {
	Content    contentJSON    `json:"content"`
	Location   *locationJSON  `json:"location,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Score      float64        `json:"score"`
	DocumentID string         `json:"documentId,omitempty"`
}

type contentJSON struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type locationJSON struct {
	Type                   string              `json:"type"`
	S3Location             *uriJSON            `json:"s3Location,omitempty"`
	CustomDocumentLocation *customLocationJSON `json:"customDocumentLocation,omitempty"`
}

type uriJSON struct {
	URI string `json:"uri"`
}

type customLocationJSON struct {
	ID string `json:"id"`
}

type retrieveResponse struct {
	RetrievalResults []retrievalResultJSON `json:"retrievalResults"`
	NextToken        string                `json:"nextToken,omitempty"`
	GuardrailAction  string                `json:"guardrailAction,omitempty"`
}

// serveRetrieve handles Retrieve: POST /knowledgebases/{kbId}/retrieve.
func serveRetrieve(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	var in retrieveRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	res, err := h.runtime.Retrieve(r.Context(), driver.RetrieveInput{
		KnowledgeBaseID: segs[1], QueryText: in.RetrievalQuery.Text, NextToken: in.NextToken,
		NumberOfResults: in.RetrievalConfiguration.numberOfResults(), Guardrail: in.GuardrailConfiguration.toDriver(),
		Filter: in.RetrievalConfiguration.filter(),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, retrieveResponse{
		RetrievalResults: toResultsJSON(res.Results), NextToken: res.NextToken, GuardrailAction: res.GuardrailAction,
	})
}

func toResultsJSON(results []driver.RetrievalResult) []retrievalResultJSON {
	out := make([]retrievalResultJSON, 0, len(results))

	for i := range results {
		r := &results[i]
		item := retrievalResultJSON{Content: contentJSON{Type: "TEXT", Text: r.Text}, Score: r.Score, DocumentID: r.DocumentID}
		item.Location = toLocationJSON(r)

		if len(r.Metadata) > 0 {
			item.Metadata = r.Metadata
		}

		out = append(out, item)
	}

	return out
}

func toLocationJSON(r *driver.RetrievalResult) *locationJSON {
	switch r.LocationType {
	case driver.LocationS3:
		return &locationJSON{Type: driver.LocationS3, S3Location: &uriJSON{URI: r.LocationURI}}
	case driver.LocationCustom:
		return &locationJSON{Type: driver.LocationCustom, CustomDocumentLocation: &customLocationJSON{ID: r.CustomDocumentID}}
	default:
		return nil
	}
}

// --- RetrieveAndGenerate ---

type ragRequest struct {
	Input struct {
		Text string `json:"text"`
	} `json:"input"`
	SessionID     string `json:"sessionId"`
	Configuration *struct {
		Type                       string `json:"type"`
		KnowledgeBaseConfiguration *struct {
			KnowledgeBaseID         string                      `json:"knowledgeBaseId"`
			ModelArn                string                      `json:"modelArn"`
			RetrievalConfiguration  *retrievalConfigurationJSON `json:"retrievalConfiguration"`
			GenerationConfiguration *struct {
				GuardrailConfiguration *guardrailRequestJSON `json:"guardrailConfiguration"`
			} `json:"generationConfiguration"`
		} `json:"knowledgeBaseConfiguration"`
		ExternalSourcesConfiguration *struct {
			ModelArn string `json:"modelArn"`
			Sources  []struct {
				SourceType  string   `json:"sourceType"`
				S3Location  *uriJSON `json:"s3Location"`
				ByteContent *struct {
					Identifier  string `json:"identifier"`
					ContentType string `json:"contentType"`
					Data        []byte `json:"data"`
				} `json:"byteContent"`
			} `json:"sources"`
		} `json:"externalSourcesConfiguration"`
	} `json:"retrieveAndGenerateConfiguration"`
}

func (in *ragRequest) toDriver() driver.RetrieveAndGenerateInput {
	out := driver.RetrieveAndGenerateInput{InputText: in.Input.Text, SessionID: in.SessionID}

	c := in.Configuration
	if c == nil {
		return out
	}

	out.Type = c.Type

	if kb := c.KnowledgeBaseConfiguration; kb != nil {
		out.KnowledgeBaseID, out.ModelARN = kb.KnowledgeBaseID, kb.ModelArn
		out.NumberOfResults = kb.RetrievalConfiguration.numberOfResults()
		out.Filter = kb.RetrievalConfiguration.filter()

		if kb.GenerationConfiguration != nil {
			out.Guardrail = kb.GenerationConfiguration.GuardrailConfiguration.toDriver()
		}
	}

	if ex := c.ExternalSourcesConfiguration; ex != nil {
		out.ModelARN = ex.ModelArn

		for _, s := range ex.Sources {
			src := driver.ExternalSource{SourceType: s.SourceType}
			if s.S3Location != nil {
				src.S3URI = s.S3Location.URI
			}

			if s.ByteContent != nil {
				src.Name, src.Text = s.ByteContent.Identifier, string(s.ByteContent.Data)
			}

			out.ExternalSources = append(out.ExternalSources, src)
		}
	}

	return out
}

type citationJSON struct {
	GeneratedResponsePart *generatedPartJSON `json:"generatedResponsePart,omitempty"`
	RetrievedReferences   []referenceJSON    `json:"retrievedReferences"`
}

type generatedPartJSON struct {
	TextResponsePart struct {
		Text string `json:"text"`
		Span struct {
			Start int32 `json:"start"`
			End   int32 `json:"end"`
		} `json:"span"`
	} `json:"textResponsePart"`
}

type referenceJSON struct {
	Content  contentJSON    `json:"content"`
	Location *locationJSON  `json:"location,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ragResponse struct {
	Output struct {
		Text string `json:"text"`
	} `json:"output"`
	SessionID       string         `json:"sessionId"`
	Citations       []citationJSON `json:"citations"`
	GuardrailAction string         `json:"guardrailAction,omitempty"`
}

func toCitationsJSON(cs []driver.Citation) []citationJSON {
	out := make([]citationJSON, 0, len(cs))

	for _, c := range cs {
		part := &generatedPartJSON{}
		part.TextResponsePart.Text = c.Text
		part.TextResponsePart.Span.Start, part.TextResponsePart.Span.End = c.Start, c.End

		out = append(out, citationJSON{GeneratedResponsePart: part, RetrievedReferences: toReferencesJSON(c.References)})
	}

	return out
}

func toReferencesJSON(rs []driver.RetrievalResult) []referenceJSON {
	refs := make([]referenceJSON, 0, len(rs))

	for i := range rs {
		ref := &rs[i]
		refs = append(refs, referenceJSON{
			Content: contentJSON{Type: "TEXT", Text: ref.Text}, Location: toLocationJSON(ref), Metadata: ref.Metadata,
		})
	}

	return refs
}

// serveRAG handles RetrieveAndGenerate: POST /retrieveAndGenerate.
func serveRAG(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	var in ragRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	res, err := h.runtime.RetrieveAndGenerate(r.Context(), in.toDriver())
	if err != nil {
		writeErr(w, err)

		return
	}

	var out ragResponse

	out.Output.Text, out.SessionID = res.Text, res.SessionID
	out.Citations, out.GuardrailAction = toCitationsJSON(res.Citations), res.GuardrailAction

	writeJSON(w, out)
}

// serveRAGStream handles RetrieveAndGenerateStream: the answer as one output
// event, then one citation event per citation.
func serveRAGStream(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	withCapability(h, w, "RetrieveAndGenerateStream", func(c driver.StreamingRetrieveAndGenerate) {
		var in ragRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		res, err := c.RetrieveAndGenerateStream(r.Context(), in.toDriver())
		if err != nil {
			writeErr(w, err)

			return
		}

		events := []streamEvent{{"output", map[string]string{"text": res.Text}}}
		for _, ct := range toCitationsJSON(res.Citations) {
			events = append(events, streamEvent{"citation", map[string]any{
				"generatedResponsePart": ct.GeneratedResponsePart, "retrievedReferences": ct.RetrievedReferences,
			}})
		}

		if res.GuardrailAction != "" {
			events = append(events, streamEvent{"guardrail", map[string]string{"action": res.GuardrailAction}})
		}

		writeEventStream(w, map[string]string{headerKBSessionID: res.SessionID}, events)
	})
}

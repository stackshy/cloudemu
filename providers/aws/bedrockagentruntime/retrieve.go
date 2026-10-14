package bedrockagentruntime

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/stackshy/cloudemu/v2/errors"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Retrieval constants from the service model and the Bedrock defaults.
const (
	defaultNumberOfResults = 5
	maxNumberOfResults     = 100
	kbIdentifierPattern    = `^[0-9a-zA-Z]{10}$|^arn:aws(-[^:]+)?:bedrock:[a-z0-9-]{1,20}:[0-9]{12}:knowledge-base/[0-9a-zA-Z]{10}$`
	kbARNMarker            = ":knowledge-base/"
	metaDataSourceID       = "x-amz-bedrock-kb-data-source-id"
	metaSourceURI          = "x-amz-bedrock-kb-source-uri"
	scoreScale             = 1000
)

var kbIdentifierRE = regexp.MustCompile(kbIdentifierPattern)

// Retrieve searches a knowledge base. Linked to the control plane it ranks the
// documents ingested into the knowledge base by word overlap with the query
// and pages through them numberOfResults at a time; alone it returns two
// simulated chunks, as before.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) Retrieve(ctx context.Context, in driver.RetrieveInput) (*driver.RetrieveResult, error) {
	filter, ferr := validateRetrieve(&in, m.cp != nil)
	if ferr != nil {
		return nil, ferr
	}

	if m.cp == nil {
		return m.simulatedRetrieve(&in), nil
	}

	kbID := knowledgeBaseID(in.KnowledgeBaseID)
	if _, err := m.cp.GetKnowledgeBase(ctx, kbID); err != nil {
		return nil, err
	}

	if action, _, err := m.applyGuardrail(ctx, in.Guardrail, guardrailInput, in.QueryText); err != nil {
		return nil, err
	} else if action == guardrailIntervened {
		return &driver.RetrieveResult{Results: []driver.RetrievalResult{}, GuardrailAction: guardrailIntervened}, nil
	}

	size := defaultNumberOfResults
	if in.NumberOfResults != nil {
		size = int(*in.NumberOfResults)
	}

	ranked := m.search(kbID, in.QueryText, filter)

	results, next, err := paginate(ranked, driver.Page{NextToken: in.NextToken}, size)
	if err != nil {
		return nil, err
	}

	out := &driver.RetrieveResult{Results: results, NextToken: next}
	if in.Guardrail != nil {
		out.GuardrailAction = guardrailNone
	}

	return out, nil
}

// validateRetrieve checks Retrieve's members and returns its filter; the id
// pattern is enforced only when the runtime is linked to the control plane
// (standalone it accepts any id, as it always did).
func validateRetrieve(in *driver.RetrieveInput, strict bool) (*retrievalFilter, error) {
	var v violations

	v.required("knowledgeBaseId", in.KnowledgeBaseID == "")

	if strict {
		v.pattern("knowledgeBaseId", in.KnowledgeBaseID, kbIdentifierRE, kbIdentifierPattern)
	}

	v.required("retrievalQuery.text", in.QueryText == "")

	v.numberOfResults("retrievalConfiguration.vectorSearchConfiguration.numberOfResults", in.NumberOfResults)

	if err := v.err(); err != nil {
		return nil, err
	}

	return parseFilter(in.Filter, "retrievalConfiguration.vectorSearchConfiguration.filter")
}

// simulatedRetrieve is the standalone behavior: two chunks echoing the query.
func (m *Mock) simulatedRetrieve(in *driver.RetrieveInput) *driver.RetrieveResult {
	base := fmt.Sprintf("s3://cloudemu-%s/kb/%s", m.opts.Region, in.KnowledgeBaseID)

	return &driver.RetrieveResult{Results: []driver.RetrievalResult{
		{Text: "Simulated knowledge-base result 1 for query: " + in.QueryText, Score: 0.95,
			LocationType: driver.LocationS3, LocationURI: base + "/doc-1.txt"},
		{Text: "Simulated knowledge-base result 2 for query: " + in.QueryText, Score: 0.82,
			LocationType: driver.LocationS3, LocationURI: base + "/doc-2.txt"},
	}}
}

// search ranks every ingested document with text in a knowledge base that
// passes the filter by the share of the query's words it contains (0..1), best
// first; ties keep the document order.
func (m *Mock) search(kbID, query string, filter *retrievalFilter) []driver.RetrievalResult {
	terms := words(query)
	docs := m.cp.IndexedDocuments(kbID)
	out := make([]driver.RetrievalResult, 0, len(docs))

	for i := range docs {
		d := &docs[i]
		if d.Text == "" || !filter.matches(d.Attributes) {
			continue
		}

		out = append(out, toRetrievalResult(d, overlap(terms, words(d.Text))))
	}

	slices.SortStableFunc(out, func(a, b driver.RetrievalResult) int { return cmp.Compare(b.Score, a.Score) })

	return out
}

func toRetrievalResult(d *badriver.IndexedDocument, score float64) driver.RetrievalResult {
	r := driver.RetrievalResult{Text: d.Text, Score: score, DataSourceID: d.DataSourceID, Metadata: map[string]any{}}

	for k, v := range d.Attributes {
		r.Metadata[k] = v
	}

	r.Metadata[metaDataSourceID] = d.DataSourceID

	switch d.Identifier.DataSourceType {
	case badriver.DocumentSourceS3:
		r.LocationType, r.LocationURI, r.DocumentID = driver.LocationS3, d.Identifier.S3URI, d.Identifier.S3URI
		r.Metadata[metaSourceURI] = d.Identifier.S3URI
	default:
		r.LocationType, r.CustomDocumentID, r.DocumentID = driver.LocationCustom, d.Identifier.CustomID, d.Identifier.CustomID
	}

	return r
}

// words lower-cases s and splits it into unique letter/digit words.
func words(s string) map[string]bool {
	out := map[string]bool{}

	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[w] = true
	}

	return out
}

// overlap is the share of query words found in doc, rounded to 3 places.
func overlap(query, doc map[string]bool) float64 {
	if len(query) == 0 {
		return 0
	}

	hit := 0

	for w := range query {
		if doc[w] {
			hit++
		}
	}

	return float64(hit*scoreScale/len(query)) / scoreScale
}

// knowledgeBaseID returns the id of a knowledge-base id or ARN.
func knowledgeBaseID(identifier string) string {
	if _, id, ok := strings.Cut(identifier, kbARNMarker); ok {
		return id
	}

	return identifier
}

// RetrieveAndGenerate answers a query from a knowledge base (KNOWLEDGE_BASE)
// or from documents in the request (EXTERNAL_SOURCES), with citations to the
// chunks it used. Without a configuration it answers as before, uncited.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) RetrieveAndGenerate(ctx context.Context, in driver.RetrieveAndGenerateInput) (
	*driver.RetrieveAndGenerateResult, error,
) {
	if err := validateRAG(&in); err != nil {
		return nil, err
	}

	sessionID, err := ragSession(in.SessionID)
	if err != nil {
		return nil, err
	}

	if in.Type == "" {
		return &driver.RetrieveAndGenerateResult{
			Text: "This is a simulated retrieve-and-generate answer to: " + in.InputText, SessionID: sessionID,
		}, nil
	}

	if action, msg, gerr := m.applyGuardrail(ctx, in.Guardrail, guardrailInput, in.InputText); gerr != nil {
		return nil, gerr
	} else if action == guardrailIntervened {
		return &driver.RetrieveAndGenerateResult{Text: msg, SessionID: sessionID, GuardrailAction: guardrailIntervened}, nil
	}

	sources, err := m.ragSources(ctx, &in)
	if err != nil {
		return nil, err
	}

	text, err := m.generate(ctx, modelIDFromARN(in.ModelARN), "", in.InputText, sources)
	if err != nil {
		return nil, err
	}

	out := &driver.RetrieveAndGenerateResult{Text: text, SessionID: sessionID, Citations: cite(text, sources)}
	if in.Guardrail != nil {
		out.GuardrailAction = guardrailNone
	}

	return out, nil
}

// RetrieveAndGenerateStream is RetrieveAndGenerate whose answer the wire layer
// streams as output and citation events.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) RetrieveAndGenerateStream(ctx context.Context, in driver.RetrieveAndGenerateInput) (
	*driver.RetrieveAndGenerateResult, error,
) {
	return m.RetrieveAndGenerate(ctx, in)
}

func validateRAG(in *driver.RetrieveAndGenerateInput) error {
	var v violations

	v.required("input.text", in.InputText == "")

	if in.SessionID != "" {
		v.sessionID(in.SessionID)
	}

	v.enum("retrieveAndGenerateConfiguration.type", in.Type, driver.RAGKnowledgeBase, driver.RAGExternalSources)

	switch in.Type {
	case driver.RAGKnowledgeBase:
		v.required("retrieveAndGenerateConfiguration.knowledgeBaseConfiguration.knowledgeBaseId", in.KnowledgeBaseID == "")
		v.required("retrieveAndGenerateConfiguration.knowledgeBaseConfiguration.modelArn", in.ModelARN == "")
		v.numberOfResults("retrieveAndGenerateConfiguration.knowledgeBaseConfiguration.retrievalConfiguration."+
			"vectorSearchConfiguration.numberOfResults", in.NumberOfResults)
	case driver.RAGExternalSources:
		v.required("retrieveAndGenerateConfiguration.externalSourcesConfiguration.modelArn", in.ModelARN == "")
		v.required("retrieveAndGenerateConfiguration.externalSourcesConfiguration.sources", len(in.ExternalSources) == 0)
	}

	return v.err()
}

// ragSources returns the chunks a RetrieveAndGenerate answer uses.
func (m *Mock) ragSources(ctx context.Context, in *driver.RetrieveAndGenerateInput) ([]driver.RetrievalResult, error) {
	if in.Type == driver.RAGExternalSources {
		return m.externalSources(ctx, in)
	}

	if m.cp == nil {
		return nil, errors.New(errors.InvalidArgument, "knowledge-base retrieval needs the bedrock-agent control plane")
	}

	kbID := knowledgeBaseID(in.KnowledgeBaseID)
	if _, err := m.cp.GetKnowledgeBase(ctx, kbID); err != nil {
		return nil, err
	}

	filter, err := parseFilter(in.Filter,
		"retrieveAndGenerateConfiguration.knowledgeBaseConfiguration.retrievalConfiguration.vectorSearchConfiguration.filter")
	if err != nil {
		return nil, err
	}

	ranked := m.search(kbID, in.InputText, filter)

	n := defaultNumberOfResults
	if in.NumberOfResults != nil {
		n = int(*in.NumberOfResults)
	}

	return ranked[:min(n, len(ranked))], nil
}

// externalSources returns the request's EXTERNAL_SOURCES as chunks, reading
// S3 sources from cloudemu S3.
func (m *Mock) externalSources(ctx context.Context, in *driver.RetrieveAndGenerateInput) ([]driver.RetrievalResult, error) {
	out := make([]driver.RetrievalResult, 0, len(in.ExternalSources))

	for _, s := range in.ExternalSources {
		if s.S3URI != "" && s.Text == "" {
			text, err := m.readS3Text(ctx, s.S3URI)
			if err != nil {
				return nil, err
			}

			s.Text = text
		}

		r := driver.RetrievalResult{Text: s.Text, Score: overlap(words(in.InputText), words(s.Text))}
		if s.S3URI != "" {
			r.LocationType, r.LocationURI = driver.LocationS3, s.S3URI
		}

		out = append(out, r)
	}

	return out, nil
}

// cite returns one citation spanning the whole answer, referencing the
// sources that share a word with it.
func cite(text string, sources []driver.RetrievalResult) []driver.Citation {
	var refs []driver.RetrievalResult

	for _, s := range sources {
		if s.Score > 0 {
			refs = append(refs, s)
		}
	}

	if len(refs) == 0 {
		return []driver.Citation{}
	}

	end := int32(len(text) - 1) //nolint:gosec // answer length is bounded by the model output.

	return []driver.Citation{{Text: text, Start: 0, End: end, References: refs}}
}

// modelIDFromARN returns the model id of a foundation-model ARN, or the input.
func modelIDFromARN(arn string) string {
	if i := strings.LastIndex(arn, "foundation-model/"); i >= 0 {
		return arn[i+len("foundation-model/"):]
	}

	return arn
}

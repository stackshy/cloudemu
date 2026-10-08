package kendra

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Documented search limits.
const (
	maxQueryTokens       = 30
	maxResultsWindow     = 100
	excerptRunes         = 200
	excerptLeadRunes     = 60
	maxPassageTokens     = 200
	maxRequestedAttrs    = 100
	maxRequestedAttrName = 200
	maxFacetPairs        = 10
)

var requestedAttrPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`)

// stopWords are dropped from a query unless nothing else is left, so a natural
// language question matches on its content words.
//
//nolint:gochecknoglobals // static lookup set
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "of": true, "to": true,
	"in": true, "on": true, "for": true, "and": true, "or": true, "what": true, "how": true, "do": true,
	"does": true, "i": true, "my": true, "we": true, "you": true, "it": true, "be": true, "can": true,
}

// tokenize lowercases text and splits it into letter/digit runs.
func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// queryTerms turns query text into its distinct search terms: at most 30
// tokens, stop words removed unless that would leave nothing.
func queryTerms(text string) []string {
	tokens := tokenize(text)
	if len(tokens) > maxQueryTokens {
		tokens = tokens[:maxQueryTokens]
	}

	seen := map[string]bool{}
	content := []string{}
	all := []string{}

	for _, t := range tokens {
		if seen[t] {
			continue
		}

		seen[t] = true

		all = append(all, t)

		if !stopWords[t] {
			content = append(content, t)
		}
	}

	if len(content) == 0 {
		return all
	}

	return content
}

// systemAttributes are the attributes the service derives for every document.
func (d *storedDocument) allAttributes() []driver.DocumentAttribute {
	out := copyAttributes(d.Attributes)

	add := func(key, val string) {
		if val == "" {
			return
		}

		for i := range out {
			if out[i].Key == key {
				return
			}
		}

		s := val
		out = append(out, driver.DocumentAttribute{Key: key, Value: driver.DocumentAttributeValue{StringValue: &s}})
	}

	add("_document_id", d.ID)
	add("_document_title", d.Title)
	add("_file_type", d.ContentType)

	return out
}

// uri is the document's source URI: the _source_uri attribute, else its S3 path.
func (d *storedDocument) uri() string {
	for i := range d.Attributes {
		if d.Attributes[i].Key == "_source_uri" && d.Attributes[i].Value.StringValue != nil {
			return *d.Attributes[i].Value.StringValue
		}
	}

	if d.S3Path != nil {
		return "s3://" + d.S3Path.Bucket + "/" + d.S3Path.Key
	}

	return ""
}

// match scores a document against the query terms: the fraction of terms found
// and the total occurrences (title counts triple).
type match struct {
	doc      storedDocument
	fraction float64
	hits     int
}

func scoreDocument(d *storedDocument, terms []string) (fraction float64, hits int) {
	if len(terms) == 0 {
		return 0, 0
	}

	title := tokenize(d.Title)
	body := tokenize(d.Text)
	found := 0

	for _, term := range terms {
		n := 0

		for _, t := range title {
			if t == term {
				n += 3
			}
		}

		for _, t := range body {
			if t == term {
				n++
			}
		}

		if n > 0 {
			found++
			hits += n
		}
	}

	return float64(found) / float64(len(terms)), hits
}

// scoreConfidence buckets a matched-term fraction into Kendra's confidence scale.
func scoreConfidence(fraction float64) string {
	switch {
	case fraction >= 1:
		return driver.ScoreVeryHigh
	case fraction >= 0.75: //nolint:mnd // bucket boundary
		return driver.ScoreHigh
	case fraction >= 0.5: //nolint:mnd // bucket boundary
		return driver.ScoreMedium
	default:
		return driver.ScoreLow
	}
}

// search returns the documents of an index that pass the filter and match the
// query terms, best first (matched fraction, occurrences, then id).
func (m *Mock) search(indexID, queryText string, filter *driver.AttributeFilter) ([]match, []string, error) {
	// A malformed filter is a ValidationException even when the index has no
	// documents to evaluate it against.
	if _, err := evalFilter(filter, nil); err != nil {
		return nil, nil, err
	}

	terms := queryTerms(queryText)
	out := []match{}

	docs := m.documentsOf(indexID)

	for i := range docs {
		d := &docs[i]

		ok, err := evalFilter(filter, d.allAttributes())
		if err != nil {
			return nil, nil, err
		}

		if !ok {
			continue
		}

		if len(terms) == 0 {
			out = append(out, match{doc: *d})

			continue
		}

		if fraction, hits := scoreDocument(d, terms); hits > 0 {
			out = append(out, match{doc: *d, fraction: fraction, hits: hits})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].fraction != out[j].fraction {
			return out[i].fraction > out[j].fraction
		}

		if out[i].hits != out[j].hits {
			return out[i].hits > out[j].hits
		}

		return out[i].doc.ID < out[j].doc.ID
	})

	return out, terms, nil
}

// highlightsIn returns the whole-word occurrences of the terms in text as
// highlights (offsets are in characters), in text order.
func highlightsIn(text string, terms []string) []driver.Highlight {
	lower := []rune(strings.ToLower(text))
	out := []driver.Highlight{}

	for _, term := range terms {
		tr := []rune(term)

		for i := 0; i+len(tr) <= len(lower); i++ {
			if string(lower[i:i+len(tr)]) != term || !wordBoundary(lower, i, i+len(tr)) {
				continue
			}

			begin, end := int32(i), int32(i+len(tr)) //nolint:gosec // offsets are bounded by the text length
			out = append(out, driver.Highlight{BeginOffset: begin, EndOffset: end, Type: "STANDARD"})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].BeginOffset < out[j].BeginOffset })

	return out
}

// wordBoundary reports whether runes[start:end] is a whole word: not preceded or
// followed by a letter or digit.
func wordBoundary(runes []rune, start, end int) bool {
	if start > 0 && (unicode.IsLetter(runes[start-1]) || unicode.IsDigit(runes[start-1])) {
		return false
	}

	return end >= len(runes) || (!unicode.IsLetter(runes[end]) && !unicode.IsDigit(runes[end]))
}

func excerpt(text string, terms []string) driver.TextWithHighlights {
	if text == "" {
		return driver.TextWithHighlights{Highlights: []driver.Highlight{}}
	}

	lower := strings.ToLower(text)

	// Find the first whole-word match with strings.Index (no per-position work)
	// and cut the window around it; only the snippet is highlighted. ToLower maps
	// rune to rune, so the match's rune index in lower is its rune index in text
	// and the window is cut from the original (a byte offset would drift where a
	// character changes byte length, e.g. İ, ẞ or the Kelvin sign).
	first := 0
	if at := firstWordMatch(lower, terms); at > 0 {
		first = utf8.RuneCountInString(lower[:at])
	}

	start := 0
	if first > excerptLeadRunes {
		start = first - excerptLeadRunes
	}

	snippet := strings.TrimSpace(runeWindow(text, start, excerptRunes))

	return driver.TextWithHighlights{Text: snippet, Highlights: highlightsIn(snippet, terms)}
}

// firstWordMatch returns the byte offset of the earliest whole-word occurrence of
// any term in the lower-cased text, or -1.
func firstWordMatch(lower string, terms []string) int {
	best := -1

	for _, term := range terms {
		for from := 0; from < len(lower); {
			i := strings.Index(lower[from:], term)
			if i < 0 {
				break
			}

			at := from + i
			if wordBoundaryBytes(lower, at, at+len(term)) {
				if best < 0 || at < best {
					best = at
				}

				break
			}

			from = at + 1
		}
	}

	return best
}

// wordBoundaryBytes is wordBoundary over byte offsets of a string.
func wordBoundaryBytes(s string, start, end int) bool {
	if start > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:start]); unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}

	if end >= len(s) {
		return true
	}

	r, _ := utf8.DecodeRuneInString(s[end:])

	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// runeWindow returns up to count runes of s starting at rune index start,
// without converting the whole string.
func runeWindow(s string, start, count int) string {
	from, n := 0, 0

	for from < len(s) && n < start {
		_, size := utf8.DecodeRuneInString(s[from:])
		from += size
		n++
	}

	to, taken := from, 0

	for to < len(s) && taken < count {
		_, size := utf8.DecodeRuneInString(s[to:])
		to += size
		taken++
	}

	return s[from:to]
}

func filterRequested(attrs []driver.DocumentAttribute, requested []string) []driver.DocumentAttribute {
	if len(requested) == 0 {
		return attrs
	}

	want := map[string]bool{}
	for _, k := range requested {
		want[k] = true
	}

	out := []driver.DocumentAttribute{}

	for i := range attrs {
		if want[attrs[i].Key] {
			out = append(out, attrs[i])
		}
	}

	return out
}

func validateRequested(requested []string) error {
	if len(requested) > maxRequestedAttrs {
		return validation("RequestedDocumentAttributes must have at most %d items", maxRequestedAttrs)
	}

	for _, k := range requested {
		if len(k) < 1 || len(k) > maxRequestedAttrName || !requestedAttrPattern.MatchString(k) {
			return validation("invalid requested document attribute %q", k)
		}
	}

	return nil
}

// pageWindow validates PageNumber/PageSize and returns the slice bounds into a
// result list of n items (only the first 100 results are retrievable).
func pageWindow(pageNumber, pageSize int32, n int) (start, end int, err error) {
	if pageNumber < 0 || pageSize < 0 {
		return 0, 0, validation("PageNumber and PageSize must not be negative")
	}

	number := int(pageNumber)
	if number == 0 {
		number = 1
	}

	size := int(pageSize)
	if size == 0 {
		size = defaultPageSize
	}

	size = min(size, maxResultsWindow)
	n = min(n, maxResultsWindow)
	start = min((number-1)*size, n)
	end = min(start+size, n)

	return start, end, nil
}

// Query searches an index. Documents are matched on their title and extracted
// text with a simple term-frequency ranking (no semantic ranking); FAQ and
// suggested-answer result types are not produced, so a result type filter of
// QUESTION_ANSWER or ANSWER returns nothing.
func (m *Mock) Query(_ context.Context, in *driver.QueryInput) (*driver.QueryOutput, error) {
	if err := validateQuery(in); err != nil {
		return nil, err
	}

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	matches, terms, err := m.search(in.IndexID, in.QueryText, in.AttributeFilter)
	if err != nil {
		return nil, err
	}

	if in.ResultTypeFilter == driver.ResultTypeQuestionAnswer || in.ResultTypeFilter == driver.ResultTypeAnswer {
		matches = nil
	}

	queryID := newUUID()

	// A featured document is returned once, in FeaturedResultsItems, so it leaves
	// the regular results on every page (keeping the paging consistent).
	featured := m.featuredItems(in.IndexID, in.QueryText, queryID, in.RequestedAttributes)
	matches = withoutFeatured(matches, featured)

	sortMatches(matches, in.Sorting)

	start, end, err := pageWindow(in.PageNumber, in.PageSize, len(matches))
	if err != nil {
		return nil, err
	}

	out := &driver.QueryOutput{
		QueryID: queryID,
		Total:   int32(len(matches)), //nolint:gosec // bounded by documents in one in-memory index
		Items:   make([]driver.QueryResultItem, 0, end-start),
		Facets:  buildFacets(matches, in.Facets),
	}

	for i := start; i < end; i++ {
		out.Items = append(out.Items, queryItem(queryID, &matches[i], terms, in.RequestedAttributes))
	}

	if in.PageNumber <= 1 {
		out.FeaturedResultsItems = featured
	}

	m.recordQueryMetrics(in.IndexID)
	m.logQuery(in.IndexID, in.QueryText, len(matches))

	return out, nil
}

// withoutFeatured drops the matches whose document is already featured.
func withoutFeatured(matches []match, featured []driver.QueryResultItem) []match {
	if len(featured) == 0 {
		return matches
	}

	ids := make(map[string]bool, len(featured))
	for i := range featured {
		ids[featured[i].DocumentID] = true
	}

	out := matches[:0:0]

	for i := range matches {
		if !ids[matches[i].doc.ID] {
			out = append(out, matches[i])
		}
	}

	return out
}

// featuredItems returns the documents of the ACTIVE featured results set whose
// query text equals the query (case-insensitive). Featured documents that are not
// in the index are skipped, and the attribute filter does not apply to them.
func (m *Mock) featuredItems(indexID, queryText, queryID string, requested []string) []driver.QueryResultItem {
	text := strings.TrimSpace(queryText)
	if text == "" {
		return nil
	}

	sets := m.featured.SortedValues()
	out := []driver.QueryResultItem{}

	for i := range sets {
		set := &sets[i]
		if set.IndexID != indexID || set.Status != driver.FeaturedActive || !hasQueryText(set.QueryTexts, text) {
			continue
		}

		for _, id := range set.FeaturedDocuments {
			doc, ok := m.documents.Get(documentKey(indexID, id))
			if !ok {
				continue
			}

			item := queryItem(queryID, &match{doc: doc}, nil, requested)
			item.ID = queryID + "-featured-" + doc.ID
			out = append(out, item)
		}
	}

	return out
}

func hasQueryText(texts []string, want string) bool {
	for _, t := range texts {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}

	return false
}

// validateQuery applies Query's request-level constraints.
func validateQuery(in *driver.QueryInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	if err := validateRequested(in.RequestedAttributes); err != nil {
		return err
	}

	switch in.ResultTypeFilter {
	case "", driver.ResultTypeDocument, driver.ResultTypeQuestionAnswer, driver.ResultTypeAnswer:
		return nil
	default:
		return validation("invalid QueryResultTypeFilter: %q", in.ResultTypeFilter)
	}
}

// queryItem renders one matched document as a Query result item.
func queryItem(queryID string, mt *match, terms, requested []string) driver.QueryResultItem {
	d := &mt.doc
	score := driver.ScoreNotAvailable

	if len(terms) > 0 {
		score = scoreConfidence(mt.fraction)
	}

	return driver.QueryResultItem{
		ID:         queryID + "-" + d.ID,
		DocumentID: d.ID,
		Type:       driver.ResultTypeDocument,
		Format:     "TEXT",
		Title:      driver.TextWithHighlights{Text: d.Title, Highlights: highlightsIn(d.Title, terms)},
		Excerpt:    excerpt(d.Text, terms),
		URI:        d.uri(),
		Attributes: filterRequested(d.allAttributes(), requested),
		Score:      score,
	}
}

func sortMatches(matches []match, sorting []driver.SortingConfig) {
	if len(sorting) == 0 {
		return
	}

	sort.SliceStable(matches, func(i, j int) bool {
		for _, s := range sorting {
			c := compareAttr(matches[i].doc.allAttributes(), matches[j].doc.allAttributes(), s.DocumentAttributeKey)
			if c == 0 {
				continue
			}

			if s.SortOrder == "DESC" {
				return c > 0
			}

			return c < 0
		}

		return false
	})
}

func attrOf(attrs []driver.DocumentAttribute, key string) (driver.DocumentAttributeValue, bool) {
	for i := range attrs {
		if attrs[i].Key == key {
			return attrs[i].Value, true
		}
	}

	return driver.DocumentAttributeValue{}, false
}

// compareAttr orders two documents by one attribute; documents without it sort
// last.
func compareAttr(a, b []driver.DocumentAttribute, key string) int {
	av, aok := attrOf(a, key)
	bv, bok := attrOf(b, key)

	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return 1
	case !bok:
		return -1
	}

	return compareValues(av, bv)
}

func compareValues(a, b driver.DocumentAttributeValue) int {
	switch {
	case a.LongValue != nil && b.LongValue != nil:
		return cmpInt(*a.LongValue, *b.LongValue)
	case a.DateValue != nil && b.DateValue != nil:
		return cmpTime(*a.DateValue, *b.DateValue)
	case a.StringValue != nil && b.StringValue != nil:
		return strings.Compare(*a.StringValue, *b.StringValue)
	}

	return 0
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return 0
	}
}

// buildFacets counts, for each requested attribute key, how many results carry
// each value, most frequent first.
func buildFacets(matches []match, facets []driver.Facet) []driver.FacetResult {
	out := make([]driver.FacetResult, 0, len(facets))

	for _, f := range facets {
		out = append(out, buildFacet(matches, f))
	}

	return out
}

// buildFacet counts the values of one attribute across the matches.
func buildFacet(matches []match, f driver.Facet) driver.FacetResult {
	counts := map[string]*driver.FacetValueCount{}
	order := []string{}
	valueType := valueTypeString

	for i := range matches {
		v, ok := attrOf(matches[i].doc.allAttributes(), f.DocumentAttributeKey)
		if !ok {
			continue
		}

		valueType = valueTypeOf(v)

		for _, label := range valueLabels(v) {
			if c, seen := counts[label]; seen {
				c.Count++

				continue
			}

			counts[label] = &driver.FacetValueCount{Value: singleValue(v, label), Count: 1}
			order = append(order, label)
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		if counts[order[i]].Count != counts[order[j]].Count {
			return counts[order[i]].Count > counts[order[j]].Count
		}

		return order[i] < order[j]
	})

	limit := int(f.MaxResults)
	if limit <= 0 || limit > maxFacetPairs {
		limit = maxFacetPairs
	}

	res := driver.FacetResult{Key: f.DocumentAttributeKey, ValueType: valueType, Counts: []driver.FacetValueCount{}}

	for _, label := range order[:min(limit, len(order))] {
		res.Counts = append(res.Counts, *counts[label])
	}

	return res
}

// Retrieve returns the passages of an index's documents that best match the
// query: each document's text is cut into passages of up to 200 tokens and each
// passage that contains a query term is returned. Documents come best first
// and a document's passages follow in text order. QueryText is required.
func (m *Mock) Retrieve(_ context.Context, in *driver.RetrieveInput) (*driver.RetrieveOutput, error) {
	if err := validateIndexID(in.IndexID); err != nil {
		return nil, err
	}

	if in.QueryText == "" {
		return nil, validation("QueryText is required")
	}

	if err := validateRequested(in.RequestedAttributes); err != nil {
		return nil, err
	}

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	matches, terms, err := m.search(in.IndexID, in.QueryText, in.AttributeFilter)
	if err != nil {
		return nil, err
	}

	items := []driver.RetrieveItem{}
	queryID := newUUID()

	for i := range matches {
		d := matches[i].doc

		for n, passage := range passages(d.Text) {
			if fraction, hits := scorePassage(passage, terms); hits > 0 {
				items = append(items, driver.RetrieveItem{
					ID:         queryID + "-" + d.ID + "-" + strconv.Itoa(n),
					DocumentID: d.ID, Title: d.Title, URI: d.uri(), Content: passage,
					Attributes: filterRequested(d.allAttributes(), in.RequestedAttributes),
					Score:      scoreConfidence(fraction),
				})
			}
		}
	}

	start, end, err := pageWindow(in.PageNumber, in.PageSize, len(items))
	if err != nil {
		return nil, err
	}

	m.recordQueryMetrics(in.IndexID)

	return &driver.RetrieveOutput{QueryID: queryID, Items: items[start:end]}, nil
}

// passages cuts text into consecutive chunks of at most 200 tokens.
func passages(text string) []string {
	fields := strings.Fields(text)
	out := []string{}

	for i := 0; i < len(fields); i += maxPassageTokens {
		out = append(out, strings.Join(fields[i:min(i+maxPassageTokens, len(fields))], " "))
	}

	return out
}

func scorePassage(passage string, terms []string) (fraction float64, hits int) {
	d := storedDocument{Text: passage}

	return scoreDocument(&d, terms)
}

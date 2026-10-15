package bedrockagentruntime

import (
	"cmp"
	"context"
	"slices"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Rerank constraints from the service model.
const (
	maxRerankSources = 1000
	maxRerankResults = 1000
)

// Rerank scores each source by the share of the query's words it contains and
// returns the best numberOfResults of them (all sources by default), best first.
// JSON sources are scored on their JSON text.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (*Mock) Rerank(_ context.Context, in driver.RerankInput) ([]driver.RerankResult, string, error) {
	if err := validateRerank(&in); err != nil {
		return nil, "", err
	}

	terms := words(in.Query)
	out := make([]driver.RerankResult, 0, len(in.Sources))

	for i, s := range in.Sources {
		text := s.Text
		if text == "" {
			text = string(s.JSON)
		}

		out = append(out, driver.RerankResult{Index: int32(i), Score: overlap(terms, words(text)), Source: s}) //nolint:gosec // i < 1000.
	}

	slices.SortStableFunc(out, func(a, b driver.RerankResult) int { return cmp.Compare(b.Score, a.Score) })

	if n := in.NumberOfResults; n != nil && int(*n) < len(out) {
		out = out[:*n]
	}

	return paginate(out, driver.Page{NextToken: in.NextToken}, len(out))
}

func validateRerank(in *driver.RerankInput) error {
	var v violations

	v.required("queries.member.textQuery.text", in.Query == "")
	v.required("rerankingConfiguration.bedrockRerankingConfiguration.modelConfiguration.modelArn", in.ModelARN == "")

	if n := len(in.Sources); n == 0 || n > maxRerankSources {
		v.addf("Value at 'sources' failed to satisfy constraint: Member must have length between 1 and %d", maxRerankSources)
	}

	if n := in.NumberOfResults; n != nil && (*n < 1 || *n > maxRerankResults) {
		v.addf("Value '%d' at 'rerankingConfiguration.bedrockRerankingConfiguration.numberOfResults' failed to satisfy "+
			"constraint: Member must have value between 1 and %d", *n, maxRerankResults)
	}

	return v.err()
}

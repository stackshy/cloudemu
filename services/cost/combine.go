package cost

import (
	"context"
	"sort"
	"time"
)

// combined is the union of several Commitments sources. It lets the billing
// engine amortize commitments that live in different backends together — AWS
// Reserved Instances (served by the EC2 handler) and Savings Plans (served by
// the Savings Plans handler) both implement Commitments, and the Cost Explorer
// reservation coverage/utilization consumer reads them as one source.
type combined struct {
	sources []Commitments
}

// Combine unions several Commitments sources into one. ListActive concatenates
// every source's active commitments at the query instant, in a deterministic
// (ID-sorted) order. Nil sources are skipped, so Combine(nil, sp) == sp's view.
// Combining zero sources yields a source that is always empty.
func Combine(sources ...Commitments) Commitments {
	kept := make([]Commitments, 0, len(sources))

	for _, s := range sources {
		if s != nil {
			kept = append(kept, s)
		}
	}

	return &combined{sources: kept}
}

// ListActive returns the union of every source's active commitments at at. The
// first error from any source is returned (with no partial result), so a caller
// never amortizes a half-read set.
func (c *combined) ListActive(ctx context.Context, at time.Time) ([]Commitment, error) {
	var out []Commitment

	for _, s := range c.sources {
		got, err := s.ListActive(ctx, at)
		if err != nil {
			return nil, err
		}

		out = append(out, got...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

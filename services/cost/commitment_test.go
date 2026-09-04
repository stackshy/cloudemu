package cost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// onDemandLine and coveredLine build the two kinds of already-tagged bucket
// line items Coverage/Utilization read: an on-demand line carries no commitment
// id, a covered line carries one. Both span a 24h bucket from start.
func onDemandLine(start time.Time, cost float64) LineItem {
	return LineItem{
		UsageStart: start, UsageEnd: start.Add(24 * time.Hour),
		UnblendedCostUSD: cost, AmortizedCostUSD: cost, PricingModel: PricingModelOnDemand,
	}
}

func coveredLine(start time.Time, cost float64) LineItem {
	return LineItem{
		UsageStart: start, UsageEnd: start.Add(24 * time.Hour),
		UnblendedCostUSD: cost, AmortizedCostUSD: cost, PricingModel: PricingModelSavingsPlan, CommitmentID: "c",
	}
}

func TestCommitmentActiveAt(t *testing.T) {
	start := day(2026, time.January, 1)
	c := Commitment{Start: start, End: start.Add(24 * time.Hour)}

	assert.True(t, c.activeAt(start), "start is inclusive")
	assert.True(t, c.activeAt(start.Add(12*time.Hour)))
	assert.False(t, c.activeAt(start.Add(24*time.Hour)), "end is exclusive")
	assert.False(t, c.activeAt(start.Add(-time.Hour)))
}

func TestPricingModel(t *testing.T) {
	assert.Equal(t, PricingModelReserved, pricingModel(KindReservedInstance))
	assert.Equal(t, PricingModelCUD, pricingModel(KindCUD))
	assert.Equal(t, PricingModelSavingsPlan, pricingModel(KindSavingsPlan))
	assert.Equal(t, PricingModelSavingsPlan, pricingModel("unknown"), "unknown kind is never on-demand")
}

// TestCoverage aggregates the covered spend read from the line tags, per bucket
// and overall. Coverage is tag-driven, so the commitments argument does not move
// the coverage numbers (it feeds Utilization's denominator instead).
func TestCoverage(t *testing.T) {
	d0 := day(2026, time.January, 1)
	d1 := day(2026, time.January, 2)

	// Day 0: $24 covered + $24 on-demand ($48 total). Day 1: $24 fully covered.
	lines := []LineItem{
		coveredLine(d0, 24), onDemandLine(d0, 24),
		coveredLine(d1, 24),
	}

	got := Coverage(lines, nil)

	assert.InDelta(t, 72, got.TotalSpendUSD, 1e-9)
	assert.InDelta(t, 48, got.CoveredSpendUSD, 1e-9)
	assert.InDelta(t, 48.0/72.0*100, got.CoveragePercent, 1e-9)

	require.Len(t, got.Buckets, 2)
	assert.True(t, got.Buckets[0].Start.Equal(d0), "buckets are ordered by start")
	assert.InDelta(t, 48, got.Buckets[0].TotalSpendUSD, 1e-9)
	assert.InDelta(t, 24, got.Buckets[0].CoveredSpendUSD, 1e-9)
	assert.InDelta(t, 50, got.Buckets[0].CoveragePercent, 1e-9)
	assert.InDelta(t, 100, got.Buckets[1].CoveragePercent, 1e-9)
}

func TestCoverage_EmptyInputs(t *testing.T) {
	got := Coverage(nil, nil)
	assert.Zero(t, got.TotalSpendUSD)
	assert.Zero(t, got.CoveragePercent)
	assert.Empty(t, got.Buckets)

	// A zero-dollar bucket yields 0% with no divide-by-zero.
	got = Coverage([]LineItem{onDemandLine(day(2026, time.January, 1), 0)}, nil)
	require.Len(t, got.Buckets, 1)
	assert.Zero(t, got.Buckets[0].CoveragePercent)
}

// TestUtilization measures the used commitment (the covered spend from the tags)
// against the available commitment (from the commitment budget at each bucket
// start).
func TestUtilization(t *testing.T) {
	d0 := day(2026, time.January, 1)
	d1 := day(2026, time.January, 2)
	end := d1.Add(24 * time.Hour)

	tests := []struct {
		name        string
		lines       []LineItem
		commitments []Commitment
		wantAvail   float64
		wantUsed    float64
		wantPercent float64
	}{
		{
			name:        "no commitment: nothing available",
			lines:       []LineItem{onDemandLine(d0, 48)},
			commitments: nil,
			wantAvail:   0, wantUsed: 0, wantPercent: 0,
		},
		{
			name: "fully utilized: every committed dollar is covered spend",
			// $1/hr*24h = $24 available each day; both days fully covered.
			lines:       []LineItem{coveredLine(d0, 24), coveredLine(d1, 24)},
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 1, Start: d0, End: end}},
			wantAvail:   48, wantUsed: 48, wantPercent: 100,
		},
		{
			name: "underutilized: big commitment, little covered spend",
			// $10/hr*24h = $240/day, $480 available; only $60 covered spend.
			lines:       []LineItem{coveredLine(d0, 48), coveredLine(d1, 12)},
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 10, Start: d0, End: end}},
			wantAvail:   480, wantUsed: 60, wantPercent: 60.0 / 480.0 * 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Utilization(tc.lines, tc.commitments)
			assert.InDelta(t, tc.wantAvail, got.TotalCommitmentUSD, 1e-9)
			assert.InDelta(t, tc.wantUsed, got.UsedCommitmentUSD, 1e-9)
			assert.InDelta(t, tc.wantPercent, got.UtilizationPercent, 1e-9)
		})
	}
}

func TestUtilization_EmptyInputs(t *testing.T) {
	got := Utilization(nil, nil)
	assert.Zero(t, got.TotalCommitmentUSD)
	assert.Zero(t, got.UtilizationPercent)
	assert.Empty(t, got.Buckets)
}

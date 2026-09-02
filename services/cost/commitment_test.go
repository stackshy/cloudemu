package cost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bucket(start time.Time, spend float64) LineItem {
	return LineItem{UsageStart: start, UsageEnd: start.Add(24 * time.Hour), UnblendedCostUSD: spend}
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

func TestCoverage(t *testing.T) {
	d0 := day(2026, time.January, 1)
	d1 := day(2026, time.January, 2)

	// Two daily buckets: day 0 spends $48, day 1 spends $24.
	lines := []LineItem{bucket(d0, 48), bucket(d1, 24)}

	tests := []struct {
		name         string
		commitments  []Commitment
		wantTotal    float64
		wantCovered  float64
		wantPercent  float64
		wantBuckets  int
		wantBucket0  float64 // covered spend in bucket 0
	}{
		{
			name:        "no commitments",
			commitments: nil,
			wantTotal:   72, wantCovered: 0, wantPercent: 0, wantBuckets: 2, wantBucket0: 0,
		},
		{
			name: "full coverage both days",
			// $2/hr * 24h = $48 available per day: day 0 fully covered, day 1 too.
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 2, Start: d0, End: d1.Add(24 * time.Hour)}},
			wantTotal:   72, wantCovered: 72, wantPercent: 100, wantBuckets: 2, wantBucket0: 48,
		},
		{
			name: "partial coverage capped at spend and at budget",
			// $1/hr * 24h = $24 available per day: day 0 covers $24 of $48, day 1 covers all $24.
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 1, Start: d0, End: d1.Add(24 * time.Hour)}},
			wantTotal:   72, wantCovered: 48, wantPercent: 48.0 / 72.0 * 100, wantBuckets: 2, wantBucket0: 24,
		},
		{
			name: "commitment only active day 1",
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 2, Start: d1, End: d1.Add(24 * time.Hour)}},
			wantTotal:   72, wantCovered: 24, wantPercent: 24.0 / 72.0 * 100, wantBuckets: 2, wantBucket0: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Coverage(lines, tc.commitments)
			assert.InDelta(t, tc.wantTotal, got.TotalSpendUSD, 1e-9)
			assert.InDelta(t, tc.wantCovered, got.CoveredSpendUSD, 1e-9)
			assert.InDelta(t, tc.wantPercent, got.CoveragePercent, 1e-9)
			require.Len(t, got.Buckets, tc.wantBuckets)
			assert.InDelta(t, tc.wantBucket0, got.Buckets[0].CoveredSpendUSD, 1e-9)
		})
	}
}

func TestCoverage_EmptyInputs(t *testing.T) {
	got := Coverage(nil, nil)
	assert.Zero(t, got.TotalSpendUSD)
	assert.Zero(t, got.CoveragePercent)
	assert.Empty(t, got.Buckets)

	// Spend but a zero-dollar bucket yields 0% (no divide-by-zero).
	got = Coverage([]LineItem{bucket(day(2026, time.January, 1), 0)}, nil)
	require.Len(t, got.Buckets, 1)
	assert.Zero(t, got.Buckets[0].CoveragePercent)
}

func TestUtilization(t *testing.T) {
	d0 := day(2026, time.January, 1)
	d1 := day(2026, time.January, 2)

	lines := []LineItem{bucket(d0, 48), bucket(d1, 12)}

	tests := []struct {
		name        string
		commitments []Commitment
		wantAvail   float64
		wantUsed    float64
		wantPercent float64
	}{
		{
			name:        "no commitment: nothing available",
			commitments: nil,
			wantAvail:   0, wantUsed: 0, wantPercent: 0,
		},
		{
			name: "fully utilized when spend exceeds commitment",
			// $1/hr*24h = $24/day, $48 total. Day 0 spends $48 (uses all $24),
			// day 1 spends $12 (uses $12 of $24).
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 1, Start: d0, End: d1.Add(24 * time.Hour)}},
			wantAvail:   48, wantUsed: 36, wantPercent: 36.0 / 48.0 * 100,
		},
		{
			name: "underutilized: big commitment, small spend",
			// $10/hr*24h = $240/day, $480 total; used = $48 + $12 = $60.
			commitments: []Commitment{{ID: "c", HourlyCommitmentUSD: 10, Start: d0, End: d1.Add(24 * time.Hour)}},
			wantAvail:   480, wantUsed: 60, wantPercent: 60.0 / 480.0 * 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Utilization(lines, tc.commitments)
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

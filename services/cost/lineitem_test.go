package cost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/pricing"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// oneVM is a single always-on VM that prices positively, plus a free bucket the
// always-on filter must drop.
func oneVM() []resourcediscovery.Resource {
	return []resourcediscovery.Resource{
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-1", SKU: "Standard_D2s_v3", Region: "eastus"},
		{Provider: "azure", Service: "storage", Type: "Bucket", ID: "blob-1", Region: "eastus"},
	}
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestLineItems_ProRationSumsToWindowCost(t *testing.T) {
	start := day(2026, time.January, 1)
	end := day(2026, time.January, 11) // 10 daily buckets

	lines, err := LineItems(context.Background(), fakeInventory{res: oneVM()}, nil, start, end, 24*time.Hour)
	require.NoError(t, err)

	// One priced resource over 10 daily buckets: the free bucket is dropped.
	require.Len(t, lines, 10)

	monthly := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil)
	hourly := monthly / hoursPerMonth

	var total float64

	for i, l := range lines {
		assert.Equal(t, "vm-1", l.ID)
		assert.Equal(t, start.Add(time.Duration(i)*24*time.Hour), l.UsageStart)
		assert.Equal(t, start.Add(time.Duration(i+1)*24*time.Hour), l.UsageEnd)
		assert.InDelta(t, hourly*24, l.UnblendedCostUSD, 1e-9, "each daily bucket bills 24 hours")
		// No commitments: on-demand, amortized == unblended.
		assert.Equal(t, PricingModelOnDemand, l.PricingModel)
		assert.Empty(t, l.CommitmentID)
		assert.InDelta(t, l.UnblendedCostUSD, l.AmortizedCostUSD, 1e-9)

		total += l.UnblendedCostUSD
	}

	// The replayed window bills exactly hourly * total-hours.
	assert.InDelta(t, hourly*end.Sub(start).Hours(), total, 1e-9)
}

func TestLineItems_PartialTrailingBucket(t *testing.T) {
	start := day(2026, time.January, 1)
	end := start.Add(36 * time.Hour) // one full day + a 12h trailing bucket

	lines, err := LineItems(context.Background(), fakeInventory{res: oneVM()}, nil, start, end, 24*time.Hour)
	require.NoError(t, err)
	require.Len(t, lines, 2)

	hourly := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil) / hoursPerMonth

	assert.InDelta(t, hourly*24, lines[0].UnblendedCostUSD, 1e-9)
	assert.Equal(t, end, lines[1].UsageEnd, "trailing bucket is clamped to end")
	assert.InDelta(t, hourly*12, lines[1].UnblendedCostUSD, 1e-9, "trailing bucket bills only 12 hours")
}

func TestLineItems_CommitmentCoversSpend(t *testing.T) {
	start := day(2026, time.January, 1)
	end := start.Add(24 * time.Hour)

	hourly := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil) / hoursPerMonth
	full := hourly * 24 // the VM's on-demand cost for the 24h bucket

	run := func(commitments []Commitment) []LineItem {
		lines, err := LineItems(context.Background(), fakeInventory{res: oneVM()}, commitments, start, end, 24*time.Hour)
		require.NoError(t, err)

		return lines
	}

	t.Run("fully covered emits one covered line", func(t *testing.T) {
		lines := run([]Commitment{{ID: "c-1", Kind: KindReservedInstance, HourlyCommitmentUSD: hourly * 2, Start: start, End: end}})
		require.Len(t, lines, 1)
		assert.Equal(t, PricingModelReserved, lines[0].PricingModel)
		assert.Equal(t, "c-1", lines[0].CommitmentID)
		assert.InDelta(t, full, lines[0].UnblendedCostUSD, 1e-9)
		assert.InDelta(t, full, lines[0].AmortizedCostUSD, 1e-9)
	})

	t.Run("exact-cost commitment covers the whole line", func(t *testing.T) {
		lines := run([]Commitment{{ID: "c-1", Kind: KindSavingsPlan, HourlyCommitmentUSD: hourly, Start: start, End: end}})
		require.Len(t, lines, 1, "epsilon keeps FP rounding from leaving a ~0 on-demand remainder")
		assert.Equal(t, "c-1", lines[0].CommitmentID)
		assert.InDelta(t, full, lines[0].UnblendedCostUSD, 1e-9)
	})

	t.Run("too-small commitment splits into covered + on-demand", func(t *testing.T) {
		lines := run([]Commitment{{ID: "c-1", Kind: KindSavingsPlan, HourlyCommitmentUSD: hourly / 2, Start: start, End: end}})
		require.Len(t, lines, 2)

		assert.Equal(t, PricingModelSavingsPlan, lines[0].PricingModel)
		assert.Equal(t, "c-1", lines[0].CommitmentID)
		assert.InDelta(t, full/2, lines[0].UnblendedCostUSD, 1e-9)

		assert.Equal(t, PricingModelOnDemand, lines[1].PricingModel)
		assert.Empty(t, lines[1].CommitmentID)
		assert.InDelta(t, full/2, lines[1].UnblendedCostUSD, 1e-9)

		assert.InDelta(t, full, lines[0].UnblendedCostUSD+lines[1].UnblendedCostUSD, 1e-9, "split preserves total cost")
	})

	t.Run("expired at bucket time leaves the line on-demand", func(t *testing.T) {
		lines := run([]Commitment{{
			ID: "c-1", Kind: KindCUD, HourlyCommitmentUSD: hourly * 10,
			Start: start.Add(-48 * time.Hour), End: start.Add(-24 * time.Hour),
		}})
		require.Len(t, lines, 1)
		assert.Equal(t, PricingModelOnDemand, lines[0].PricingModel)
		assert.Empty(t, lines[0].CommitmentID)
		assert.InDelta(t, full, lines[0].UnblendedCostUSD, 1e-9)
	})
}

// TestLineItems_FractionalCoverageDivergenceCase pins the exact scenario the
// old whole-line-first-fit logic got wrong: two identical priced lines and a
// commitment budget that lands between one and two of them. The boundary line
// must SPLIT, and Coverage (which reads the tags) must agree with the
// continuous dollar coverage — the two algorithms can no longer diverge.
func TestLineItems_FractionalCoverageDivergenceCase(t *testing.T) {
	start := day(2026, time.January, 1)
	end := start.Add(24 * time.Hour)

	res := []resourcediscovery.Resource{
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-a", SKU: "Standard_D2s_v3", Region: "eastus"},
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-b", SKU: "Standard_D2s_v3", Region: "eastus"},
	}

	hourly := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil) / hoursPerMonth
	lineCost := hourly * 24               // each VM's cost for the 24h bucket
	budget := 1.5 * lineCost              // covers vm-a fully + half of vm-b
	commitments := []Commitment{{ID: "c-1", Kind: KindSavingsPlan, HourlyCommitmentUSD: 1.5 * hourly, Start: start, End: end}}

	lines, err := LineItems(context.Background(), fakeInventory{res: res}, commitments, start, end, 24*time.Hour)
	require.NoError(t, err)

	// vm-a: one covered line. vm-b: split into covered + on-demand. Old logic
	// would have left vm-b entirely on-demand (2 lines) — this asserts 3.
	require.Len(t, lines, 3)

	assert.Equal(t, "vm-a", lines[0].ID)
	assert.Equal(t, "c-1", lines[0].CommitmentID)
	assert.InDelta(t, lineCost, lines[0].UnblendedCostUSD, 1e-9)

	assert.Equal(t, "vm-b", lines[1].ID)
	assert.Equal(t, PricingModelSavingsPlan, lines[1].PricingModel)
	assert.Equal(t, "c-1", lines[1].CommitmentID)
	assert.InDelta(t, 0.5*lineCost, lines[1].UnblendedCostUSD, 1e-9)

	assert.Equal(t, "vm-b", lines[2].ID)
	assert.Equal(t, PricingModelOnDemand, lines[2].PricingModel)
	assert.Empty(t, lines[2].CommitmentID)
	assert.InDelta(t, 0.5*lineCost, lines[2].UnblendedCostUSD, 1e-9)

	// Total cost preserved across the split.
	var total float64
	for _, l := range lines {
		total += l.UnblendedCostUSD
	}

	assert.InDelta(t, 2*lineCost, total, 1e-9)

	// Coverage, reading the tags, reports the exact continuous coverage: $1.5L
	// covered of $2L = 75%. This is the invariant that used to break.
	cov := Coverage(lines, commitments)
	assert.InDelta(t, budget, cov.CoveredSpendUSD, 1e-9)
	assert.InDelta(t, 2*lineCost, cov.TotalSpendUSD, 1e-9)
	assert.InDelta(t, 75.0, cov.CoveragePercent, 1e-9)

	// Utilization: the full commitment was used (spend exceeds it just barely
	// on the covered side; here used == available == budget).
	util := Utilization(lines, commitments)
	assert.InDelta(t, budget, util.TotalCommitmentUSD, 1e-9)
	assert.InDelta(t, budget, util.UsedCommitmentUSD, 1e-9)
	assert.InDelta(t, 100.0, util.UtilizationPercent, 1e-9)
}

func TestLineItems_CommitmentPartialAcrossLines(t *testing.T) {
	start := day(2026, time.January, 1)
	end := start.Add(24 * time.Hour)

	res := []resourcediscovery.Resource{
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-a", SKU: "Standard_D2s_v3", Region: "eastus"},
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-b", SKU: "Standard_D2s_v3", Region: "eastus"},
	}

	hourly := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil) / hoursPerMonth
	// Budget covers exactly one of the two identical VMs for the 24h bucket.
	commitments := []Commitment{{ID: "c-1", Kind: KindSavingsPlan, HourlyCommitmentUSD: hourly, Start: start, End: end}}

	lines, err := LineItems(context.Background(), fakeInventory{res: res}, commitments, start, end, 24*time.Hour)
	require.NoError(t, err)
	require.Len(t, lines, 2)

	// Sorted by id: vm-a is covered first, vm-b spills to on-demand.
	assert.Equal(t, "vm-a", lines[0].ID)
	assert.Equal(t, "c-1", lines[0].CommitmentID)
	assert.Equal(t, PricingModelSavingsPlan, lines[0].PricingModel)

	assert.Equal(t, "vm-b", lines[1].ID)
	assert.Empty(t, lines[1].CommitmentID)
	assert.Equal(t, PricingModelOnDemand, lines[1].PricingModel)
}

func TestLineItems_EdgeCases(t *testing.T) {
	start := day(2026, time.January, 1)
	end := start.Add(24 * time.Hour)

	t.Run("nil inventory", func(t *testing.T) {
		lines, err := LineItems(context.Background(), nil, nil, start, end, time.Hour)
		require.NoError(t, err)
		assert.Nil(t, lines)
	})

	t.Run("non-positive bucket", func(t *testing.T) {
		_, err := LineItems(context.Background(), fakeInventory{res: oneVM()}, nil, start, end, 0)
		require.Error(t, err)
		assert.True(t, cerrors.IsInvalidArgument(err))
	})

	t.Run("start not before end", func(t *testing.T) {
		lines, err := LineItems(context.Background(), fakeInventory{res: oneVM()}, nil, end, start, time.Hour)
		require.NoError(t, err)
		assert.Nil(t, lines)
	})

	t.Run("only zero-cost resources", func(t *testing.T) {
		res := []resourcediscovery.Resource{{Provider: "azure", Service: "storage", Type: "Bucket", ID: "b-1", Region: "eastus"}}
		lines, err := LineItems(context.Background(), fakeInventory{res: res}, nil, start, end, time.Hour)
		require.NoError(t, err)
		assert.Empty(t, lines)
	})

	t.Run("inventory error propagates", func(t *testing.T) {
		sentinel := errors.New("boom")
		_, err := LineItems(context.Background(), fakeInventory{err: sentinel}, nil, start, end, time.Hour)
		require.ErrorIs(t, err, sentinel)
	})
}

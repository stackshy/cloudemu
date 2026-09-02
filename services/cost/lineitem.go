package cost

import (
	"context"
	"sort"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/pricing"
)

// hoursPerMonth is the reference month length used to pro-rate a resource's
// monthly estimate into an hourly rate. It mirrors pricing.HoursPerMonth so the
// replay below is consistent with how pricing.Monthly built the estimate: a
// resource priced at $M/month bills $M/hoursPerMonth for every wall-clock hour.
const hoursPerMonth = pricing.HoursPerMonth

// LineItem is one priced resource's spend within a single [UsageStart,UsageEnd)
// time bucket — the emulator's analog of a Cost and Usage Report / FOCUS row.
// It carries both the on-demand (unblended) cost and the commitment-amortized
// cost so the FinOps wire surfaces (AWS Cost Explorer / CUR, Azure Cost
// Management) can shape either view without re-pricing.
type LineItem struct {
	Provider string
	Service  string
	Type     string
	ID       string
	ARN      string
	Region   string

	UsageStart time.Time
	UsageEnd   time.Time

	// UnblendedCostUSD is the on-demand spend the resource booked in this
	// bucket, pro-rated from its monthly estimate. It is set for every line
	// regardless of commitment coverage.
	UnblendedCostUSD float64
	// AmortizedCostUSD is the effective cost after commitment amortization. For
	// on-demand (uncovered) usage it equals UnblendedCostUSD; for usage a
	// commitment absorbed it is the dollar amount of the commitment attributed
	// to this line (equal to the displaced on-demand spend under the dollar-
	// commitment model this foundation uses).
	AmortizedCostUSD float64
	// PricingModel is OnDemand for uncovered usage, otherwise the covering
	// commitment's model (SavingsPlan / Reserved / CUD).
	PricingModel string
	// CommitmentID is the id of the commitment that covered this line, empty for
	// on-demand usage.
	CommitmentID string
}

// LineItems replays the inventory's priced resources across [start, end) at
// bucket granularity, producing one LineItem per priced resource per bucket. It
// is deterministic: it uses only the passed start/end/bucket window and never
// consults the wall clock, so a FakeClock-driven caller gets stable output.
//
// Each resource's monthly estimate is pro-rated to an hourly rate
// (monthly / hoursPerMonth) and billed for the wall-clock hours in each bucket,
// so a short trailing bucket bills proportionally less. Zero-cost (usage-based
// or free) resources are dropped, matching Estimate.
//
// Commitments are applied per bucket in resource-id order: the pooled dollar
// commitment active at the bucket start covers whole lines until it is
// exhausted, tagging each covered line with the commitment's model and id. A
// nil inventory yields no line items; a non-positive bucket is rejected; a
// window with start >= end yields no line items.
func LineItems(
	ctx context.Context,
	inv Inventory,
	commitments []Commitment,
	start, end time.Time,
	bucket time.Duration,
) ([]LineItem, error) {
	if bucket <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "cost: bucket duration must be positive")
	}

	if inv == nil || !start.Before(end) {
		return nil, nil
	}

	priced, _, err := Estimate(ctx, inv)
	if err != nil {
		return nil, err
	}

	if len(priced) == 0 {
		return nil, nil
	}

	sort.Slice(priced, func(i, j int) bool { return priced[i].ID < priced[j].ID })

	var out []LineItem

	for t := start; t.Before(end); t = t.Add(bucket) {
		bucketEnd := t.Add(bucket)
		if bucketEnd.After(end) {
			bucketEnd = end
		}

		out = append(out, bucketLines(priced, commitments, t, bucketEnd)...)
	}

	return out, nil
}

// bucketLines builds the pro-rated, commitment-attributed line items for a
// single [start,end) bucket. The priced resources are assumed pre-sorted by id.
func bucketLines(priced []Line, commitments []Commitment, start, end time.Time) []LineItem {
	hours := end.Sub(start).Hours()

	// remaining tracks unspent dollar commitment per active commitment for this
	// bucket, walked in id order so attribution is deterministic.
	active := activeCommitments(commitments, start)
	remaining := make([]float64, len(active))

	for i := range active {
		remaining[i] = active[i].HourlyCommitmentUSD * hours
	}

	lines := make([]LineItem, 0, len(priced))

	for i := range priced {
		cost := priced[i].MonthlyUSD / hoursPerMonth * hours

		li := LineItem{
			Provider:         priced[i].Provider,
			Service:          priced[i].Service,
			Type:             priced[i].Type,
			ID:               priced[i].ID,
			ARN:              priced[i].ARN,
			Region:           priced[i].Region,
			UsageStart:       start,
			UsageEnd:         end,
			UnblendedCostUSD: cost,
			AmortizedCostUSD: cost,
			PricingModel:     PricingModelOnDemand,
		}

		applyCommitment(&li, active, remaining, cost)

		lines = append(lines, li)
	}

	return lines
}

// applyCommitment tags li with the first active commitment whose remaining
// dollar budget can absorb the whole line, deducting the cost from that budget.
// A line the pooled budget cannot fully cover stays on-demand.
func applyCommitment(li *LineItem, active []Commitment, remaining []float64, cost float64) {
	for j := range active {
		if remaining[j] >= cost {
			remaining[j] -= cost
			li.PricingModel = pricingModel(active[j].Kind)
			li.CommitmentID = active[j].ID

			return
		}
	}
}

// activeCommitments returns the commitments in force at instant t, sorted by id
// for deterministic attribution.
func activeCommitments(commitments []Commitment, t time.Time) []Commitment {
	var active []Commitment

	for i := range commitments {
		if commitments[i].activeAt(t) {
			active = append(active, commitments[i])
		}
	}

	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })

	return active
}

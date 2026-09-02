package cost

import (
	"context"
	"sort"
	"time"
)

// Commitment kinds. Every provider's prepaid-discount instrument maps onto one
// of these so a single amortization engine serves AWS Savings Plans / Reserved
// Instances, Azure reservations, and GCP committed-use discounts (CUDs).
const (
	KindSavingsPlan      = "SavingsPlan"      // AWS Savings Plan
	KindReservedInstance = "ReservedInstance" // AWS RI / Azure reservation
	KindCUD              = "CUD"              // GCP committed-use discount
)

// Pricing models a priced usage line can carry. Uncovered usage is OnDemand;
// usage covered by a commitment carries the commitment's model.
const (
	PricingModelOnDemand    = "OnDemand"
	PricingModelSavingsPlan = "SavingsPlan"
	PricingModelReserved    = "Reserved"
	PricingModelCUD         = "CUD"
)

// percentScale turns a 0..1 ratio into a 0..100 percentage.
const percentScale = 100.0

// Commitment is a provider-agnostic prepaid-discount instrument. It commits to
// HourlyCommitmentUSD of spend for every hour in [Start, End). AWS SP/RI, Azure
// reservations, and GCP CUDs are all normalized into this one shape so the
// Coverage/Utilization/amortization math lives here once, provider-agnostic.
//
// The commitment is expressed as a dollar commitment (Savings-Plan style): each
// active hour it can absorb up to HourlyCommitmentUSD of on-demand spend.
type Commitment struct {
	ID                  string
	Provider            string
	Kind                string // one of the Kind* constants
	Scope               string // optional applicability scope (region, account, …)
	HourlyCommitmentUSD float64
	Start               time.Time
	End                 time.Time
}

// activeAt reports whether the commitment is in force at instant t. Start is
// inclusive and End exclusive, matching how a per-bucket sample is attributed.
func (c *Commitment) activeAt(t time.Time) bool {
	return !t.Before(c.Start) && t.Before(c.End)
}

// pricingModel maps a commitment kind to the pricing model a covered usage line
// carries. An unknown kind falls back to SavingsPlan (the dollar-commitment
// default), never OnDemand, since a covered line is by definition committed.
func pricingModel(kind string) string {
	switch kind {
	case KindReservedInstance:
		return PricingModelReserved
	case KindCUD:
		return PricingModelCUD
	default:
		return PricingModelSavingsPlan
	}
}

// Commitments is the read-only source of purchased commitments the cost engine
// amortizes, mirroring the Inventory IoC pattern: it is satisfied later by each
// provider's purchase store (Savings Plan / reservation / CUD), so this package
// never imports a provider package. ListActive returns the commitments in force
// at instant at.
type Commitments interface {
	ListActive(ctx context.Context, at time.Time) ([]Commitment, error)
}

// CoverageBucket is the coverage of one time bucket: how much on-demand spend
// occurred and how much of it a commitment absorbed.
type CoverageBucket struct {
	Start           time.Time
	End             time.Time
	TotalSpendUSD   float64
	CoveredSpendUSD float64
	CoveragePercent float64 // CoveredSpendUSD / TotalSpendUSD * 100; 0 when no spend
}

// CoverageResult is the aggregate coverage across all buckets plus the per-
// bucket breakdown. CoveragePercent answers "what share of my spend was on a
// commitment rather than on demand?".
type CoverageResult struct {
	TotalSpendUSD   float64
	CoveredSpendUSD float64
	CoveragePercent float64
	Buckets         []CoverageBucket
}

// UtilizationBucket is the utilization of one time bucket: how much commitment
// was available and how much of it was actually consumed by spend.
type UtilizationBucket struct {
	Start              time.Time
	End                time.Time
	CommitmentUSD      float64
	UsedCommitmentUSD  float64
	UtilizationPercent float64 // UsedCommitmentUSD / CommitmentUSD * 100; 0 when none available
}

// UtilizationResult is the aggregate utilization across all buckets plus the
// per-bucket breakdown. UtilizationPercent answers "what share of the
// commitment I paid for did my spend actually use?".
type UtilizationResult struct {
	TotalCommitmentUSD float64
	UsedCommitmentUSD  float64
	UtilizationPercent float64
	Buckets            []UtilizationBucket
}

// bucketMetric is one contiguous [start,end) window's spend accounting: the
// total on-demand spend booked in it, how much of that spend a commitment
// covered (read from the line items' own tags), and the dollar commitment on
// offer. Coverage and Utilization both read from this one derivation, so the
// aggregate percentages can never disagree with the per-line tags.
type bucketMetric struct {
	start     time.Time
	end       time.Time
	total     float64
	covered   float64
	available float64
}

// perBucketMetrics folds the line items into per-window accounting keyed by the
// [UsageStart, UsageEnd) pair, in ascending start order for deterministic
// output. covered is summed from the lines already tagged with a commitment
// (CommitmentID set) — the single source of truth — so a caller that generated
// the lines via LineItems sees coverage that matches those tags exactly.
// available is the dollar commitment sampled at the bucket start.
func perBucketMetrics(lines []LineItem, commitments []Commitment) []bucketMetric {
	type key struct {
		start int64
		end   int64
	}

	index := map[key]int{}

	var buckets []bucketMetric

	for i := range lines {
		k := key{start: lines[i].UsageStart.UnixNano(), end: lines[i].UsageEnd.UnixNano()}

		pos, ok := index[k]
		if !ok {
			pos = len(buckets)
			index[k] = pos

			buckets = append(buckets, bucketMetric{start: lines[i].UsageStart, end: lines[i].UsageEnd})
		}

		buckets[pos].total += lines[i].UnblendedCostUSD
		if lines[i].CommitmentID != "" {
			buckets[pos].covered += lines[i].UnblendedCostUSD
		}
	}

	for i := range buckets {
		buckets[i].available = availableCommitmentUSD(buckets[i].start, buckets[i].end, commitments)
	}

	sort.Slice(buckets, func(i, j int) bool { return buckets[i].start.Before(buckets[j].start) })

	return buckets
}

// availableCommitmentUSD is the dollar commitment on offer for a [start,end)
// bucket: the summed hourly commitment of every commitment active at the bucket
// start, scaled by the bucket's length in hours. A partial (short) bucket
// therefore offers proportionally less commitment.
func availableCommitmentUSD(start, end time.Time, commitments []Commitment) float64 {
	hours := end.Sub(start).Hours()
	if hours <= 0 {
		return 0
	}

	var perHour float64

	for i := range commitments {
		if commitments[i].activeAt(start) {
			perHour += commitments[i].HourlyCommitmentUSD
		}
	}

	return perHour * hours
}

// Coverage reports how much of the line items' on-demand spend a commitment
// absorbed, per time bucket and in aggregate. It is a pure function of the two
// inputs. Covered spend is read from the lines already tagged by LineItems
// (CommitmentID set), which applies the commitment fractionally and
// splits the boundary line, so within a bucket:
//
//	covered = min(spend, availableCommitment)
//
// holds by construction — the aggregate percentage never disagrees with the
// per-line tags. A bucket with no spend contributes 0% coverage.
func Coverage(lines []LineItem, commitments []Commitment) CoverageResult {
	var res CoverageResult

	for _, b := range perBucketMetrics(lines, commitments) {
		res.Buckets = append(res.Buckets, CoverageBucket{
			Start:           b.start,
			End:             b.end,
			TotalSpendUSD:   b.total,
			CoveredSpendUSD: b.covered,
			CoveragePercent: percent(b.covered, b.total),
		})

		res.TotalSpendUSD += b.total
		res.CoveredSpendUSD += b.covered
	}

	res.CoveragePercent = percent(res.CoveredSpendUSD, res.TotalSpendUSD)

	return res
}

// Utilization reports how much of the purchased commitment the line items'
// spend actually consumed, per time bucket and in aggregate. It is a pure
// function of the two inputs. The consumed commitment equals the covered spend
// read from the line tags (the same single source of truth Coverage uses),
// while the available commitment is sampled at each bucket's start. A bucket
// with no available commitment contributes 0% utilization.
func Utilization(lines []LineItem, commitments []Commitment) UtilizationResult {
	var res UtilizationResult

	for _, b := range perBucketMetrics(lines, commitments) {
		res.Buckets = append(res.Buckets, UtilizationBucket{
			Start:              b.start,
			End:                b.end,
			CommitmentUSD:      b.available,
			UsedCommitmentUSD:  b.covered,
			UtilizationPercent: percent(b.covered, b.available),
		})

		res.TotalCommitmentUSD += b.available
		res.UsedCommitmentUSD += b.covered
	}

	res.UtilizationPercent = percent(res.UsedCommitmentUSD, res.TotalCommitmentUSD)

	return res
}

// percent returns part/whole as a 0..100 percentage, or 0 when whole is zero.
func percent(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}

	return part / whole * percentScale
}

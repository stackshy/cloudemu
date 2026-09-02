package savingsplans

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// Savings Plan lifecycle states (a subset of the SavingsPlanState enum). A plan
// purchased for immediate effect lands active; one with a future purchaseTime is
// queued until then; DeleteQueuedSavingsPlan moves a queued plan to
// queued-deleted. The emulator settles payment instantly, so no plan lingers in
// payment-pending.
const (
	stateActive        = "active"
	stateQueued        = "queued"
	stateQueuedDeleted = "queued-deleted"
	stateRetired       = "retired"
)

// Savings Plan types (SavingsPlanType enum).
const (
	planTypeCompute     = "Compute"
	planTypeEC2Instance = "EC2Instance"
	planTypeSageMaker   = "SageMaker"
)

// Payment options (SavingsPlanPaymentOption enum).
const (
	paymentAllUpfront     = "All Upfront"
	paymentPartialUpfront = "Partial Upfront"
	paymentNoUpfront      = "No Upfront"
)

// currencyUSD is the only currency the seeded catalog prices in.
const currencyUSD = "USD"

// Term durations in seconds: a one-year and a three-year commitment.
const (
	termOneYear   = int64(365 * 24 * 60 * 60)
	termThreeYear = 3 * termOneYear
)

// savingsPlan is one purchased Savings Plan. State lives only in the wire server
// (no portable driver represents Savings Plans), so the handler owns this shape
// directly rather than a three-layer provider mock.
type savingsPlan struct {
	ID            string            `json:"id"`
	ARN           string            `json:"arn"`
	OfferingID    string            `json:"offeringId"`
	Commitment    string            `json:"commitment"` // hourly commitment, e.g. "1.000"
	Currency      string            `json:"currency"`
	EC2Family     string            `json:"ec2InstanceFamily,omitempty"`
	PlanType      string            `json:"planType"`
	PaymentOption string            `json:"paymentOption"`
	ProductTypes  []string          `json:"productTypes,omitempty"`
	Region        string            `json:"region"`
	Upfront       string            `json:"upfront,omitempty"`
	Recurring     string            `json:"recurring,omitempty"`
	Description   string            `json:"description,omitempty"`
	TermSeconds   int64             `json:"termSeconds"`
	Start         time.Time         `json:"start"`
	End           time.Time         `json:"end"`
	State         string            `json:"state"`
	Tags          map[string]string `json:"tags,omitempty"`
}

// offering is one entry in the seeded DescribeSavingsPlansOfferings catalog. A
// CreateSavingsPlan request names an offering by id; the plan inherits the
// offering's type, term, payment option and product mix.
type offering struct {
	id            string
	description   string
	durationSecs  int64
	paymentOption string
	planType      string
	productTypes  []string
	serviceCode   string
	usageType     string
	operation     string
	ec2Family     string
}

// store is the in-memory backing state for the Savings Plans wire handler. The
// plan set is a memstore.Store so it snapshots/restores like every other backend
// (persist #582). The offering catalog is static (seeded at New), and the
// client-token index makes CreateSavingsPlan idempotent.
type store struct {
	mu        sync.Mutex
	clock     config.Clock
	accountID string
	region    string

	plans     *memstore.Store[*savingsPlan]
	offerings []offering
	byToken   map[string]string // clientToken -> savingsPlanId, for idempotency
}

// newStore returns a Savings Plans store with the offering catalog seeded. A nil
// clock falls back to the real clock so deterministic tests can inject a FakeClock.
func newStore(accountID, region string, clock config.Clock) *store {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &store{
		clock:     clock,
		accountID: accountID,
		region:    region,
		plans:     memstore.New[*savingsPlan](),
		offerings: seedOfferings(),
		byToken:   map[string]string{},
	}
}

// seedOfferings returns a representative Savings Plans offering catalog spanning
// the Compute / EC2Instance / SageMaker plan types, both term lengths and all
// three payment options.
func seedOfferings() []offering {
	return []offering{
		{
			id: "sp-offering-compute-1yr-no", description: "Compute Savings Plan, 1 year, No Upfront",
			durationSecs: termOneYear, paymentOption: paymentNoUpfront, planType: planTypeCompute,
			productTypes: []string{"EC2", "Fargate", "Lambda"}, serviceCode: "ComputeSavingsPlans",
			usageType: "ComputeSP:1yrNoUpfront", operation: "Hourly",
		},
		{
			id: "sp-offering-compute-3yr-all", description: "Compute Savings Plan, 3 year, All Upfront",
			durationSecs: termThreeYear, paymentOption: paymentAllUpfront, planType: planTypeCompute,
			productTypes: []string{"EC2", "Fargate", "Lambda"}, serviceCode: "ComputeSavingsPlans",
			usageType: "ComputeSP:3yrAllUpfront", operation: "Hourly",
		},
		{
			id: "sp-offering-ec2-1yr-partial", description: "EC2 Instance Savings Plan, 1 year, Partial Upfront",
			durationSecs: termOneYear, paymentOption: paymentPartialUpfront, planType: planTypeEC2Instance,
			productTypes: []string{"EC2"}, serviceCode: "AmazonEC2", usageType: "EC2SP:1yrPartialUpfront",
			operation: "RunInstances", ec2Family: "m5",
		},
		{
			id: "sp-offering-sagemaker-1yr-no", description: "SageMaker Savings Plan, 1 year, No Upfront",
			durationSecs: termOneYear, paymentOption: paymentNoUpfront, planType: planTypeSageMaker,
			productTypes: []string{"SageMaker"}, serviceCode: "AmazonSageMaker",
			usageType: "SageMakerSP:1yrNoUpfront", operation: "RunTrainingJob",
		},
	}
}

// findOffering returns the seeded offering with the given id.
func (s *store) findOffering(id string) (*offering, bool) {
	for i := range s.offerings {
		if s.offerings[i].id == id {
			return &s.offerings[i], true
		}
	}

	return nil, false
}

// create purchases a Savings Plan against a seeded offering. A future
// purchaseTime queues the plan (state queued, effective from that time);
// otherwise it is immediately active. clientToken makes the call idempotent: a
// repeated token returns the plan already created for it. The commitment is the
// caller's hourly dollar commitment; upfront is echoed back when supplied.
func (s *store) create(in *createInput) (string, error) {
	off, ok := s.findOffering(in.savingsPlanOfferingID)
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument, "offering not found: %s", in.savingsPlanOfferingID)
	}

	if in.commitment == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "commitment is required")
	}

	if _, err := strconv.ParseFloat(in.commitment, 64); err != nil {
		return "", cerrors.Newf(cerrors.InvalidArgument, "invalid commitment: %s", in.commitment)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if in.clientToken != "" {
		if id, seen := s.byToken[in.clientToken]; seen {
			return id, nil
		}
	}

	now := s.clock.Now().UTC()
	start := now
	state := stateActive

	if in.purchaseTime.After(now) {
		start = in.purchaseTime.UTC()
		state = stateQueued
	}

	id := idgen.UUID()
	plan := &savingsPlan{
		ID:            id,
		ARN:           s.planARN(id),
		OfferingID:    off.id,
		Commitment:    in.commitment,
		Currency:      currencyUSD,
		EC2Family:     off.ec2Family,
		PlanType:      off.planType,
		PaymentOption: off.paymentOption,
		ProductTypes:  off.productTypes,
		Region:        s.region,
		Upfront:       in.upfrontPaymentAmount,
		Recurring:     recurringFor(off.paymentOption),
		Description:   off.description,
		TermSeconds:   off.durationSecs,
		Start:         start,
		End:           start.Add(time.Duration(off.durationSecs) * time.Second),
		State:         state,
		Tags:          copyTags(in.tags),
	}

	s.plans.Set(id, plan)

	if in.clientToken != "" {
		s.byToken[in.clientToken] = id
	}

	return id, nil
}

// effectiveState resolves a plan's current state from the clock rather than a
// value frozen at creation: a plan is queued until its Start, active in
// [Start, End), and retired at or after End. The queued-deleted lifecycle is
// terminal and sticky — a plan deleted while queued stays queued-deleted and
// never becomes active — so it (and any other stored non-time state such as a
// payment-pending/failed marker, were one modeled) short-circuits the time rule.
func effectiveState(p *savingsPlan, now time.Time) string {
	if p.State == stateQueuedDeleted {
		return stateQueuedDeleted
	}

	switch {
	case now.Before(p.Start):
		return stateQueued
	case now.Before(p.End):
		return stateActive
	default:
		return stateRetired
	}
}

// recurringFor reports the recurring payment amount a payment option carries.
// Only Partial Upfront bills a recurring hourly charge in this model; the seeded
// catalog leaves the concrete figure to the caller's commitment, so a nominal
// marker keeps the field non-empty for Partial while All/No Upfront omit it.
func recurringFor(paymentOption string) string {
	if paymentOption == paymentPartialUpfront {
		return "0.0"
	}

	return ""
}

// deleteQueued moves a queued plan to queued-deleted. A plan that is not queued
// cannot be deleted this way, matching DeleteQueuedSavingsPlan.
func (s *store) deleteQueued(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	plan, ok := s.plans.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "savings plan not found: %s", id)
	}

	if state := effectiveState(plan, s.clock.Now().UTC()); state != stateQueued {
		return cerrors.Newf(cerrors.FailedPrecondition, "savings plan %s is %s, not queued", id, state)
	}

	plan.State = stateQueuedDeleted
	s.plans.Set(id, plan)

	return nil
}

// describe returns the plans matching the filter, in ascending id order for
// deterministic output. Each returned plan is a copy whose State is the
// clock-derived effective state (queued/active/retired unless terminally
// queued-deleted), so DescribeSavingsPlans reflects lifecycle transitions
// without any stored state ever being mutated on a read.
func (s *store) describe(f planFilter) []*savingsPlan {
	all := s.plans.All()
	now := s.clock.Now().UTC()
	out := make([]*savingsPlan, 0, len(all))

	for _, p := range all {
		state := effectiveState(p, now)
		if !f.matches(p, state) {
			continue
		}

		view := *p
		view.State = state
		out = append(out, &view)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out
}

// tagsOf returns a copy of a plan's tags, or nil if the plan is unknown.
func (s *store) tagsOf(arn string) (map[string]string, bool) {
	p, ok := s.byARN(arn)
	if !ok {
		return nil, false
	}

	return copyTags(p.Tags), true
}

// tag merges tags onto the plan named by arn.
func (s *store) tag(arn string, tags map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byARN(arn)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "savings plan not found: %s", arn)
	}

	if p.Tags == nil {
		p.Tags = map[string]string{}
	}

	for k, v := range tags {
		p.Tags[k] = v
	}

	s.plans.Set(p.ID, p)

	return nil
}

// untag removes the named tag keys from the plan named by arn.
func (s *store) untag(arn string, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byARN(arn)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "savings plan not found: %s", arn)
	}

	for _, k := range keys {
		delete(p.Tags, k)
	}

	s.plans.Set(p.ID, p)

	return nil
}

// byARN finds a plan by its ARN. Callers that mutate hold s.mu; the read-only
// tagsOf path does not, and memstore's own lock keeps All()/Get() safe.
func (s *store) byARN(arn string) (*savingsPlan, bool) {
	for _, p := range s.plans.All() {
		if p.ARN == arn {
			return p, true
		}
	}

	return nil, false
}

func (s *store) planARN(id string) string {
	// Savings Plan ARNs are global (no region segment):
	// arn:aws:savingsplans::<account>:savingsplan/<id>.
	return idgen.AWSARN("savingsplans", "", s.accountID, "savingsplan/"+id)
}

// ListActive satisfies cost.Commitments: it returns every Savings Plan whose
// clock-derived effective state at instant at is active, normalized to the
// provider-agnostic cost.Commitment shape the billing engine amortizes. State is
// resolved from at (not creation), so a plan bought with a future purchaseTime
// starts feeding commitments once at reaches its Start, and an expired plan drops
// out at its End. A later Cost Explorer coverage/utilization handler
// (billing-parity step 3) reads real purchased commitments through this.
func (s *store) ListActive(_ context.Context, at time.Time) ([]cost.Commitment, error) {
	var out []cost.Commitment

	for _, p := range s.plans.All() {
		if effectiveState(p, at) != stateActive {
			continue
		}

		hourly, err := strconv.ParseFloat(p.Commitment, 64)
		if err != nil {
			continue
		}

		out = append(out, cost.Commitment{
			ID:                  p.ID,
			Provider:            "aws",
			Kind:                cost.KindSavingsPlan,
			Scope:               p.Region,
			HourlyCommitmentUSD: hourly,
			Start:               p.Start,
			End:                 p.End,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

func copyTags(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

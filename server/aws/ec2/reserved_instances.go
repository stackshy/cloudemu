package ec2

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// Reserved Instance lifecycle states (a subset of the ReservedInstanceState
// enum). State is never frozen at purchase time — it is derived from the clock
// on every read (effectiveState): queued before start, active in [start, end),
// retired at or after end. A source RI retired by a modification is terminally
// retired and short-circuits the time rule.
const (
	riStateActive  = "active"
	riStateQueued  = "queued"
	riStateRetired = "retired"
)

// Reserved Instance scope (Scope enum): whether the reservation applies across a
// Region or is pinned to a single Availability Zone.
const (
	scopeRegion = "Region"
	scopeAZ     = "Availability Zone"
)

// Offering classes (OfferingClassType enum).
const (
	offeringClassStandard    = "standard"
	offeringClassConvertible = "convertible"
)

// Offering types (OfferingTypeValues enum) — the modern payment-option-named
// offering types.
const (
	offeringTypeAllUpfront     = "All Upfront"
	offeringTypePartialUpfront = "Partial Upfront"
	offeringTypeNoUpfront      = "No Upfront"
)

// Term durations in seconds: a one-year and a three-year commitment.
const (
	riTermOneYear   = int64(365 * 24 * 60 * 60)
	riTermThreeYear = 3 * riTermOneYear
)

// riCurrencyUSD is the only currency the seeded catalog prices in (ISO 4217).
const riCurrencyUSD = "USD"

// recurringFrequencyHourly is the RecurringCharge frequency for a per-hour charge.
const recurringFrequencyHourly = "Hourly"

// Default RIProductDescription values the seeded catalog quotes.
const (
	productLinuxUNIX = "Linux/UNIX"
	productWindows   = "Windows"
)

// defaultRIRegion tags reservations and Commitment scope when the handler is
// constructed without an explicit region.
const defaultRIRegion = "us-east-1"

// reservedInstance is one purchased Reserved Instance. State lives only in the
// wire server (no portable driver represents RIs — it is a billing instrument,
// not a compute resource), so the EC2 handler owns this shape directly, exactly
// as the Savings Plans handler owns its plan shape.
type reservedInstance struct {
	id                 string
	offeringID         string
	instanceType       string
	availabilityZone   string
	scope              string
	duration           int64
	start              time.Time
	end                time.Time
	fixedPrice         float64
	usagePrice         float64
	recurringHourly    float64
	instanceCount      int32
	productDescription string
	instanceTenancy    string
	offeringClass      string
	offeringType       string
	currencyCode       string
	tags               map[string]string
	// retired marks a source RI that a ModifyReservedInstances request replaced.
	// It is terminal and sticky, short-circuiting the clock-derived state so a
	// modified reservation never reports active again.
	retired bool
}

// effectiveState resolves an RI's current state from now rather than a value
// frozen at purchase: queued until start, active in [start, end), retired at or
// after end. A reservation retired by a modification stays retired regardless of
// the clock.
func (ri *reservedInstance) effectiveState(now time.Time) string {
	if ri.retired {
		return riStateRetired
	}

	switch {
	case now.Before(ri.start):
		return riStateQueued
	case now.Before(ri.end):
		return riStateActive
	default:
		return riStateRetired
	}
}

// riOffering is one entry in the seeded DescribeReservedInstancesOfferings
// catalog. A PurchaseReservedInstancesOffering request names an offering by id;
// the purchased reservation inherits the offering's terms.
type riOffering struct {
	id                 string
	instanceType       string
	availabilityZone   string
	scope              string
	duration           int64
	fixedPrice         float64
	usagePrice         float64
	recurringHourly    float64
	productDescription string
	instanceTenancy    string
	offeringClass      string
	offeringType       string
	marketplace        bool
	// pricingTiers are the volume-discount PricingDetail rows (count, price).
	pricingTiers []pricingTier
}

// pricingTier is one PricingDetail row: the number of reservations available at
// the given per-instance price.
type pricingTier struct {
	count int32
	price float64
}

// riModification is one recorded ModifyReservedInstances request. The emulator
// settles instantly, so a recorded modification lands "fulfilled" immediately —
// no request lingers in "processing".
type riModification struct {
	id            string
	clientToken   string
	status        string
	createDate    time.Time
	updateDate    time.Time
	effectiveDate time.Time
	sourceIDs     []string
	results       []modificationResult
}

// modificationResult links a target configuration to the RI created to satisfy
// it. reservedInstancesID is populated because the emulator fulfills instantly.
type modificationResult struct {
	reservedInstancesID string
	availabilityZone    string
	instanceCount       int32
	instanceType        string
	platform            string
	scope               string
}

// riStore is the in-memory backing state for the EC2 handler's Reserved Instance
// surface. The purchased set and modification log are memstore.Stores; the
// offering catalog is static (seeded at construction). It is wire-server state
// (like the Savings Plans store), not a provider mock, so it needs no persist
// Snapshottable wiring.
type riStore struct {
	mu     sync.Mutex
	clock  config.Clock
	region string

	instances     *memstore.Store[*reservedInstance]
	modifications *memstore.Store[*riModification]
	offerings     []riOffering
	byModToken    map[string]string // clientToken -> modificationId, for idempotency
}

// newRIStore returns a Reserved Instance store with the offering catalog seeded.
// A nil clock falls back to the real clock so deterministic tests inject a
// FakeClock; an empty region falls back to defaultRIRegion.
func newRIStore(region string, clock config.Clock) *riStore {
	if clock == nil {
		clock = config.RealClock{}
	}

	if region == "" {
		region = defaultRIRegion
	}

	return &riStore{
		clock:         clock,
		region:        region,
		instances:     memstore.New[*reservedInstance](),
		modifications: memstore.New[*riModification](),
		offerings:     seedRIOfferings(region),
		byModToken:    map[string]string{},
	}
}

// seedRIOfferings returns a representative Reserved Instance offering catalog
// spanning the standard/convertible offering classes, both term lengths, all
// three payment (offering) types, Region and AZ scopes, and the Linux/UNIX and
// Windows product descriptions.
func seedRIOfferings(region string) []riOffering {
	az := region + "a"

	return []riOffering{
		{
			id: "649fd0c8-1yr-std-noupfront", instanceType: "t3.medium", scope: scopeRegion,
			duration: riTermOneYear, recurringHourly: 0.0308,
			productDescription: productLinuxUNIX, instanceTenancy: tenancyDefault,
			offeringClass: offeringClassStandard, offeringType: offeringTypeNoUpfront,
			pricingTiers: []pricingTier{{count: 10, price: 0}},
		},
		{
			id: "438012d3-3yr-std-allupfront", instanceType: "m5.large", scope: scopeRegion,
			duration: riTermThreeYear, fixedPrice: 1200,
			productDescription: productLinuxUNIX, instanceTenancy: tenancyDefault,
			offeringClass: offeringClassStandard, offeringType: offeringTypeAllUpfront,
			pricingTiers: []pricingTier{{count: 10, price: 1200}, {count: 40, price: 1150}},
		},
		{
			id: "7c3e91a0-1yr-conv-partial", instanceType: "m5.large", availabilityZone: az, scope: scopeAZ,
			duration: riTermOneYear, fixedPrice: 480, recurringHourly: 0.028,
			productDescription: productLinuxUNIX, instanceTenancy: tenancyDefault,
			offeringClass: offeringClassConvertible, offeringType: offeringTypePartialUpfront,
			pricingTiers: []pricingTier{{count: 10, price: 480}},
		},
		{
			id: "b19a44f5-1yr-std-noupfront-win", instanceType: "c5.xlarge", scope: scopeRegion,
			duration: riTermOneYear, usagePrice: 0.10, recurringHourly: 0.121,
			productDescription: productWindows, instanceTenancy: tenancyDefault,
			offeringClass: offeringClassStandard, offeringType: offeringTypeNoUpfront,
			pricingTiers: []pricingTier{{count: 10, price: 0}},
		},
	}
}

// findOffering returns the seeded offering with the given id.
func (s *riStore) findOffering(id string) (*riOffering, bool) {
	for i := range s.offerings {
		if s.offerings[i].id == id {
			return &s.offerings[i], true
		}
	}

	return nil, false
}

// purchaseInput is the decoded PurchaseReservedInstancesOffering request.
type purchaseInput struct {
	offeringID    string
	instanceCount int32
	limitPrice    *float64
	purchaseTime  time.Time
}

// purchase buys a Reserved Instance against a seeded offering. A future
// purchaseTime queues the reservation (state queued, effective from that time);
// otherwise it is immediately active. A LimitPrice below the offering's total
// fixed price is rejected (marketplace price protection), matching the real API.
func (s *riStore) purchase(in *purchaseInput) (string, error) {
	off, ok := s.findOffering(in.offeringID)
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument, "reserved instances offering not found: %s", in.offeringID)
	}

	count := in.instanceCount
	if count < 1 {
		count = 1
	}

	if in.limitPrice != nil && off.fixedPrice*float64(count) > *in.limitPrice {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"total price exceeds LimitPrice %.2f", *in.limitPrice)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now().UTC()
	start := now

	if in.purchaseTime.After(now) {
		start = in.purchaseTime.UTC()
	}

	id := idgen.UUID()
	s.instances.Set(id, &reservedInstance{
		id:                 id,
		offeringID:         off.id,
		instanceType:       off.instanceType,
		availabilityZone:   off.availabilityZone,
		scope:              off.scope,
		duration:           off.duration,
		start:              start,
		end:                start.Add(time.Duration(off.duration) * time.Second),
		fixedPrice:         off.fixedPrice,
		usagePrice:         off.usagePrice,
		recurringHourly:    off.recurringHourly,
		instanceCount:      count,
		productDescription: off.productDescription,
		instanceTenancy:    off.instanceTenancy,
		offeringClass:      off.offeringClass,
		offeringType:       off.offeringType,
		currencyCode:       riCurrencyUSD,
	})

	return id, nil
}

// describe returns the reservations matching f, in ascending id order for
// deterministic output. Each returned reservation is a copy carrying its
// clock-derived effective state resolved at now, so a describe reflects
// lifecycle transitions without mutating stored state.
func (s *riStore) describe(f riFilter, now time.Time) []*reservedInstance {
	all := s.instances.All()
	out := make([]*reservedInstance, 0, len(all))

	for _, ri := range all {
		if !f.matches(ri, ri.effectiveState(now)) {
			continue
		}

		view := *ri
		out = append(out, &view)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })

	return out
}

// modifyInput is the decoded ModifyReservedInstances request.
type modifyInput struct {
	clientToken   string
	reservedIDs   []string
	targetConfigs []targetConfig
}

// targetConfig is one requested ReservedInstancesConfiguration target.
type targetConfig struct {
	availabilityZone string
	instanceCount    int32
	instanceType     string
	platform         string
	scope            string
}

// modify records a ModifyReservedInstances request and settles it instantly:
// each target configuration mints a new active reservation (inheriting the
// source terms, with the AZ/count/type/platform/scope overrides applied) and the
// source reservations are terminally retired. The net hourly commitment is
// preserved — the new reservations replace the retired sources in the
// Commitments feed. A repeated clientToken is idempotent.
func (s *riStore) modify(in *modifyInput) (string, error) {
	if len(in.reservedIDs) == 0 || len(in.targetConfigs) == 0 {
		return "", cerrors.New(cerrors.InvalidArgument,
			"ModifyReservedInstances requires reserved instance ids and target configurations")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if in.clientToken != "" {
		if id, seen := s.byModToken[in.clientToken]; seen {
			return id, nil
		}
	}

	now := s.clock.Now().UTC()
	sources := make([]*reservedInstance, 0, len(in.reservedIDs))

	for _, id := range in.reservedIDs {
		ri, ok := s.instances.Get(id)
		if !ok {
			return "", cerrors.Newf(cerrors.NotFound, "reserved instance not found: %s", id)
		}

		if state := ri.effectiveState(now); state != riStateActive {
			return "", cerrors.Newf(cerrors.FailedPrecondition,
				"reserved instance %s is %s, only active reservations can be modified", id, state)
		}

		sources = append(sources, ri)
	}

	return s.applyModification(in, sources, now), nil
}

// applyModification mints the target reservations, retires the sources and
// records the (instantly fulfilled) modification. The caller holds s.mu.
func (s *riStore) applyModification(in *modifyInput, sources []*reservedInstance, now time.Time) string {
	tmpl := sources[0]
	results := make([]modificationResult, 0, len(in.targetConfigs))

	for _, tc := range in.targetConfigs {
		newRI := reservationFromTarget(tmpl, tc, now)
		s.instances.Set(newRI.id, newRI)

		results = append(results, modificationResult{
			reservedInstancesID: newRI.id,
			availabilityZone:    newRI.availabilityZone,
			instanceCount:       newRI.instanceCount,
			instanceType:        newRI.instanceType,
			platform:            newRI.productDescription,
			scope:               newRI.scope,
		})
	}

	sourceIDs := make([]string, len(sources))

	for i, src := range sources {
		src.retired = true
		s.instances.Set(src.id, src)
		sourceIDs[i] = src.id
	}

	modID := idgen.UUID()
	s.modifications.Set(modID, &riModification{
		id:            modID,
		clientToken:   in.clientToken,
		status:        "fulfilled",
		createDate:    now,
		updateDate:    now,
		effectiveDate: now,
		sourceIDs:     sourceIDs,
		results:       results,
	})

	if in.clientToken != "" {
		s.byModToken[in.clientToken] = modID
	}

	return modID
}

// reservationFromTarget mints a new active reservation from a source template
// with a target configuration's overrides applied. The new reservation keeps the
// source's remaining term (same end), so the modification neither extends nor
// shortens the commitment.
func reservationFromTarget(tmpl *reservedInstance, tc targetConfig, now time.Time) *reservedInstance {
	newRI := *tmpl
	newRI.id = idgen.UUID()
	newRI.retired = false
	newRI.start = now
	newRI.tags = nil

	if tc.availabilityZone != "" {
		newRI.availabilityZone = tc.availabilityZone
	}

	if tc.instanceCount > 0 {
		newRI.instanceCount = tc.instanceCount
	}

	if tc.instanceType != "" {
		newRI.instanceType = tc.instanceType
	}

	if tc.platform != "" {
		newRI.productDescription = tc.platform
	}

	if tc.scope != "" {
		newRI.scope = tc.scope
	}

	return &newRI
}

// describeModifications returns the recorded modifications matching the id and
// client-token filters, in ascending id order.
func (s *riStore) describeModifications(ids, clientTokens []string) []*riModification {
	idSet := toStringSet(ids)
	tokenSet := toStringSet(clientTokens)

	all := s.modifications.All()
	out := make([]*riModification, 0, len(all))

	for _, m := range all {
		if len(idSet) > 0 {
			if _, ok := idSet[m.id]; !ok {
				continue
			}
		}

		if len(tokenSet) > 0 {
			if _, ok := tokenSet[m.clientToken]; !ok {
				continue
			}
		}

		out = append(out, m)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })

	return out
}

// ListActive satisfies cost.Commitments: it returns every Reserved Instance
// whose clock-derived effective state at instant at is active, normalized to the
// provider-agnostic cost.Commitment shape the billing engine amortizes. The
// hourly commitment is the reservation's recurring hourly charge times its
// instance count — the effective dollar commitment per hour. State is resolved
// from at (not purchase), so an RI bought with a future purchaseTime starts
// feeding commitments once at reaches its start, and an expired reservation
// drops out at its end. A later Cost Explorer coverage/utilization handler
// (billing-parity step 3) reads real purchased commitments through this,
// alongside the Savings Plans source (union them via cost.Combine).
func (s *riStore) ListActive(_ context.Context, at time.Time) ([]cost.Commitment, error) {
	var out []cost.Commitment

	for _, ri := range s.instances.All() {
		if ri.effectiveState(at) != riStateActive {
			continue
		}

		out = append(out, cost.Commitment{
			ID:                  ri.id,
			Provider:            "aws",
			Kind:                cost.KindReservedInstance,
			Scope:               s.region,
			HourlyCommitmentUSD: ri.recurringHourly * float64(ri.instanceCount),
			Start:               ri.start,
			End:                 ri.end,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

func toStringSet(items []string) map[string]struct{} {
	if len(items) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(items))
	for _, i := range items {
		out[i] = struct{}{}
	}

	return out
}

package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server"
	ec2srv "github.com/stackshy/cloudemu/v2/server/aws/ec2"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// newRIClient wires a standalone EC2 handler (no compute/VPC driver — the
// Reserved Instance surface is wire-only) with a FakeClock the test controls,
// and returns a real aws-sdk-go-v2 EC2 client plus the handler and clock. The
// handler is returned so tests can read its cost.Commitments feed directly, the
// seam the Cost Explorer reservation consumer (billing-parity step 3) uses.
func newRIClient(t *testing.T) (*awsec2.Client, *ec2srv.Handler, *cloudconfig.FakeClock) {
	t.Helper()

	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	h := ec2srv.New(nil, nil, "000000000000", ec2srv.WithClock(fc), ec2srv.WithRegion("us-east-1"))

	srv := server.New()
	srv.Register(h)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awsec2.NewFromConfig(cfg), h, fc
}

// TestReservedInstancesOfferingsAndPurchase exercises the offering catalog, an
// immediate purchase (active now) and the DescribeReservedInstances readback
// through the real SDK wire path.
func TestReservedInstancesOfferingsAndPurchase(t *testing.T) {
	ctx := context.Background()
	client, h, fc := newRIClient(t)

	offs, err := client.DescribeReservedInstancesOfferings(ctx, &awsec2.DescribeReservedInstancesOfferingsInput{})
	if err != nil {
		t.Fatalf("DescribeReservedInstancesOfferings: %v", err)
	}

	if len(offs.ReservedInstancesOfferings) == 0 {
		t.Fatal("offerings catalog is empty, want a seeded set")
	}

	// The seeded catalog must expose volume-tier pricing details and recurring
	// charges — the fields a real client reads when comparing offerings.
	var offeringID string

	for i := range offs.ReservedInstancesOfferings {
		o := offs.ReservedInstancesOfferings[i]
		if aws.ToString(o.ReservedInstancesOfferingId) == "649fd0c8-1yr-std-noupfront" {
			offeringID = aws.ToString(o.ReservedInstancesOfferingId)

			if len(o.PricingDetails) == 0 {
				t.Error("offering missing pricingDetailsSet")
			}

			if len(o.RecurringCharges) == 0 {
				t.Error("no-upfront offering missing recurringCharges")
			}
		}
	}

	if offeringID == "" {
		t.Fatal("seeded offering 649fd0c8-1yr-std-noupfront not found")
	}

	pur, err := client.PurchaseReservedInstancesOffering(ctx, &awsec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String(offeringID),
		InstanceCount:               aws.Int32(3),
	})
	if err != nil {
		t.Fatalf("PurchaseReservedInstancesOffering: %v", err)
	}

	riID := aws.ToString(pur.ReservedInstancesId)
	if riID == "" {
		t.Fatal("purchase returned no reservedInstancesId")
	}

	desc, err := client.DescribeReservedInstances(ctx, &awsec2.DescribeReservedInstancesInput{
		ReservedInstancesIds: []string{riID},
	})
	if err != nil {
		t.Fatalf("DescribeReservedInstances: %v", err)
	}

	if len(desc.ReservedInstances) != 1 {
		t.Fatalf("describe returned %d reservations, want 1", len(desc.ReservedInstances))
	}

	ri := desc.ReservedInstances[0]
	if ri.State != ec2types.ReservedInstanceStateActive {
		t.Fatalf("purchased-now reservation state = %q, want active", ri.State)
	}

	if aws.ToInt32(ri.InstanceCount) != 3 {
		t.Fatalf("instanceCount = %d, want 3", aws.ToInt32(ri.InstanceCount))
	}

	// The commitment feed reports the reservation as active immediately, with an
	// hourly commitment of recurringHourly (0.0308) * instanceCount (3).
	assertActiveCommitment(t, h, fc.Now(), riID, 0.0308*3)
}

// TestReservedInstancesLazyClockState pins the clock-derived lifecycle a future
// purchase walks through — queued, then active once the clock reaches its start,
// then retired once past its end — asserting both the wire DescribeReservedInstances
// state AND the cost.Commitments feed at each instant (the #944 clock-advance gap).
func TestReservedInstancesLazyClockState(t *testing.T) {
	ctx := context.Background()
	client, h, fc := newRIClient(t)

	// A one-year offering purchased with a future purchaseTime (30 days out).
	start := fc.Now().Add(30 * 24 * time.Hour)

	pur, err := client.PurchaseReservedInstancesOffering(ctx, &awsec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String("649fd0c8-1yr-std-noupfront"),
		InstanceCount:               aws.Int32(1),
		PurchaseTime:                aws.Time(start),
	})
	if err != nil {
		t.Fatalf("PurchaseReservedInstancesOffering: %v", err)
	}

	riID := aws.ToString(pur.ReservedInstancesId)

	// Before start: queued on the wire, absent from the commitment feed.
	if got := describeState(ctx, t, client, riID); got != ec2types.ReservedInstanceStateQueued {
		t.Fatalf("state before start = %q, want queued", got)
	}

	assertNoCommitment(t, h, fc.Now(), riID)

	// Advance past start: active on the wire, present in the feed.
	fc.Advance(31 * 24 * time.Hour)

	if got := describeState(ctx, t, client, riID); got != ec2types.ReservedInstanceStateActive {
		t.Fatalf("state after start = %q, want active", got)
	}

	assertActiveCommitment(t, h, fc.Now(), riID, 0.0308)

	// Advance past end (one year total): retired on the wire, excluded from the feed.
	fc.Advance(365 * 24 * time.Hour)

	if got := describeState(ctx, t, client, riID); got != ec2types.ReservedInstanceStateRetired {
		t.Fatalf("state after end = %q, want retired", got)
	}

	assertNoCommitment(t, h, fc.Now(), riID)
}

// TestReservedInstancesCombineWithSavingsPlans proves the RI commitment source
// unions cleanly with another source via cost.Combine — the seam the Cost
// Explorer consumer uses to price RI and Savings Plans commitments together.
func TestReservedInstancesCombineWithSavingsPlans(t *testing.T) {
	ctx := context.Background()
	client, h, fc := newRIClient(t)

	if _, err := client.PurchaseReservedInstancesOffering(ctx, &awsec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String("649fd0c8-1yr-std-noupfront"),
		InstanceCount:               aws.Int32(2),
	}); err != nil {
		t.Fatalf("purchase: %v", err)
	}

	other := staticCommitments{{ID: "sp-1", Kind: cost.KindSavingsPlan, HourlyCommitmentUSD: 1.0,
		Start: fc.Now().Add(-time.Hour), End: fc.Now().Add(time.Hour)}}

	combined := cost.Combine(h.Commitments(), other)

	got, err := combined.ListActive(ctx, fc.Now())
	if err != nil {
		t.Fatalf("combined ListActive: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("combined feed returned %d commitments, want 2 (1 RI + 1 SP)", len(got))
	}

	var haveRI, haveSP bool

	for _, c := range got {
		switch c.Kind {
		case cost.KindReservedInstance:
			haveRI = true
		case cost.KindSavingsPlan:
			haveSP = true
		}
	}

	if !haveRI || !haveSP {
		t.Fatalf("combined feed missing a kind: RI=%v SP=%v", haveRI, haveSP)
	}
}

// TestReservedInstancesModify settles a modification instantly: the source is
// retired and a new active reservation with the target configuration replaces
// it, preserving the net commitment.
func TestReservedInstancesModify(t *testing.T) {
	ctx := context.Background()
	client, h, fc := newRIClient(t)

	pur, err := client.PurchaseReservedInstancesOffering(ctx, &awsec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String("649fd0c8-1yr-std-noupfront"),
		InstanceCount:               aws.Int32(4),
	})
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}

	sourceID := aws.ToString(pur.ReservedInstancesId)

	mod, err := client.ModifyReservedInstances(ctx, &awsec2.ModifyReservedInstancesInput{
		ReservedInstancesIds: []string{sourceID},
		TargetConfigurations: []ec2types.ReservedInstancesConfiguration{
			{AvailabilityZone: aws.String("us-east-1b"), InstanceCount: aws.Int32(4)},
		},
	})
	if err != nil {
		t.Fatalf("ModifyReservedInstances: %v", err)
	}

	if aws.ToString(mod.ReservedInstancesModificationId) == "" {
		t.Fatal("modify returned no modification id")
	}

	mods, err := client.DescribeReservedInstancesModifications(ctx,
		&awsec2.DescribeReservedInstancesModificationsInput{})
	if err != nil {
		t.Fatalf("DescribeReservedInstancesModifications: %v", err)
	}

	if len(mods.ReservedInstancesModifications) != 1 {
		t.Fatalf("got %d modifications, want 1", len(mods.ReservedInstancesModifications))
	}

	rec := mods.ReservedInstancesModifications[0]
	if got := aws.ToString(rec.Status); got != "fulfilled" {
		t.Fatalf("modification status = %q, want fulfilled (instant settle)", got)
	}

	// The source id round-trips through the nested reservedInstancesSet list, and
	// the modification result names a newly minted target reservation.
	if len(rec.ReservedInstancesIds) != 1 || aws.ToString(rec.ReservedInstancesIds[0].ReservedInstancesId) != sourceID {
		t.Fatalf("modification reservedInstancesIds = %+v, want [%s]", rec.ReservedInstancesIds, sourceID)
	}

	if len(rec.ModificationResults) != 1 || aws.ToString(rec.ModificationResults[0].ReservedInstancesId) == "" {
		t.Fatalf("modification result missing target reservation id: %+v", rec.ModificationResults)
	}

	// The source is retired; exactly one active reservation (the target) remains,
	// so the commitment feed still reports a single active RI.
	if got := describeState(ctx, t, client, sourceID); got != ec2types.ReservedInstanceStateRetired {
		t.Fatalf("source state after modify = %q, want retired", got)
	}

	active, err := h.Commitments().ListActive(ctx, fc.Now())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}

	if len(active) != 1 {
		t.Fatalf("active commitments after modify = %d, want 1 (target replaces source)", len(active))
	}
}

// describeState reads one reservation's clock-derived state through the wire.
func describeState(
	ctx context.Context, t *testing.T, client *awsec2.Client, riID string,
) ec2types.ReservedInstanceState {
	t.Helper()

	desc, err := client.DescribeReservedInstances(ctx, &awsec2.DescribeReservedInstancesInput{
		ReservedInstancesIds: []string{riID},
	})
	if err != nil {
		t.Fatalf("DescribeReservedInstances: %v", err)
	}

	if len(desc.ReservedInstances) != 1 {
		t.Fatalf("describe returned %d reservations, want 1", len(desc.ReservedInstances))
	}

	return desc.ReservedInstances[0].State
}

// assertActiveCommitment asserts the feed reports riID active at instant at with
// the expected hourly commitment.
func assertActiveCommitment(t *testing.T, h *ec2srv.Handler, at time.Time, riID string, wantHourly float64) {
	t.Helper()

	got, err := h.Commitments().ListActive(context.Background(), at)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}

	for _, c := range got {
		if c.ID != riID {
			continue
		}

		if c.Kind != cost.KindReservedInstance {
			t.Errorf("commitment kind = %q, want ReservedInstance", c.Kind)
		}

		const eps = 1e-9
		if diff := c.HourlyCommitmentUSD - wantHourly; diff > eps || diff < -eps {
			t.Errorf("hourly commitment = %v, want %v", c.HourlyCommitmentUSD, wantHourly)
		}

		return
	}

	t.Fatalf("reservation %s not active in commitment feed at %v", riID, at)
}

// assertNoCommitment asserts riID is absent from the feed at instant at.
func assertNoCommitment(t *testing.T, h *ec2srv.Handler, at time.Time, riID string) {
	t.Helper()

	got, err := h.Commitments().ListActive(context.Background(), at)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}

	for _, c := range got {
		if c.ID == riID {
			t.Fatalf("reservation %s must not be in the commitment feed at %v (state not active)", riID, at)
		}
	}
}

// staticCommitments is a fixed cost.Commitments source for exercising cost.Combine.
type staticCommitments []cost.Commitment

func (s staticCommitments) ListActive(_ context.Context, at time.Time) ([]cost.Commitment, error) {
	var out []cost.Commitment

	for _, c := range s {
		if !at.Before(c.Start) && at.Before(c.End) {
			out = append(out, c)
		}
	}

	return out, nil
}

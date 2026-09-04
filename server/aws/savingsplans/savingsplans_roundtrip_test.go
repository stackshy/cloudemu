package savingsplans_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	testAccountID      = "123456789012"
	computeOfferingID  = "sp-offering-compute-1yr-no"
	oneYearTermSeconds = int64(365 * 24 * 60 * 60)
)

// newClient stands up an in-process AWS server with the Savings Plans handler
// enabled and returns a real Savings Plans SDK client pointed at it, plus the
// fake clock driving its queued/active timeline.
func newClient(t *testing.T) (*savingsplans.Client, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC))

	srv := awsserver.New(awsserver.Drivers{
		SavingsPlans: true,
		AccountID:    testAccountID,
		Region:       "us-east-1",
		Clock:        clock,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := savingsplans.NewFromConfig(cfg, func(o *savingsplans.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, clock
}

func TestCreateAndDescribeActive(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	out, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("1.500"),
		ClientToken:           aws.String("token-1"),
		Tags:                  map[string]string{"team": "platform"},
	})
	if err != nil {
		t.Fatalf("CreateSavingsPlan: %v", err)
	}

	if aws.ToString(out.SavingsPlanId) == "" {
		t.Fatal("CreateSavingsPlan returned empty savingsPlanId")
	}

	planID := aws.ToString(out.SavingsPlanId)

	// Idempotency: the same client token returns the same plan id.
	again, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("1.500"),
		ClientToken:           aws.String("token-1"),
	})
	if err != nil {
		t.Fatalf("CreateSavingsPlan (repeat): %v", err)
	}

	if aws.ToString(again.SavingsPlanId) != planID {
		t.Fatalf("client-token idempotency broken: %s != %s", aws.ToString(again.SavingsPlanId), planID)
	}

	desc, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{})
	if err != nil {
		t.Fatalf("DescribeSavingsPlans: %v", err)
	}

	if len(desc.SavingsPlans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(desc.SavingsPlans))
	}

	p := desc.SavingsPlans[0]
	if p.State != sptypes.SavingsPlanStateActive {
		t.Fatalf("expected active, got %q", p.State)
	}

	if aws.ToString(p.Commitment) != "1.500" {
		t.Fatalf("commitment mismatch: %q", aws.ToString(p.Commitment))
	}

	if p.SavingsPlanType != sptypes.SavingsPlanTypeCompute {
		t.Fatalf("plan type mismatch: %q", p.SavingsPlanType)
	}

	if p.TermDurationInSeconds != oneYearTermSeconds {
		t.Fatalf("term mismatch: %d", p.TermDurationInSeconds)
	}

	// Filter by state should still surface the active plan.
	filtered, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{
		States: []sptypes.SavingsPlanState{sptypes.SavingsPlanStateActive},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlans (states): %v", err)
	}

	if len(filtered.SavingsPlans) != 1 {
		t.Fatalf("state filter: expected 1, got %d", len(filtered.SavingsPlans))
	}
}

func TestDescribeOfferingsCatalog(t *testing.T) {
	c, _ := newClient(t)

	out, err := c.DescribeSavingsPlansOfferings(context.Background(),
		&savingsplans.DescribeSavingsPlansOfferingsInput{})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings: %v", err)
	}

	if len(out.SearchResults) == 0 {
		t.Fatal("expected a seeded offerings catalog, got none")
	}

	var foundCompute bool

	for _, o := range out.SearchResults {
		if aws.ToString(o.OfferingId) == computeOfferingID {
			foundCompute = true

			if o.PlanType != sptypes.SavingsPlanTypeCompute {
				t.Fatalf("compute offering has wrong plan type: %q", o.PlanType)
			}

			if o.DurationSeconds != oneYearTermSeconds {
				t.Fatalf("compute offering has wrong duration: %d", o.DurationSeconds)
			}
		}
	}

	if !foundCompute {
		t.Fatalf("seeded compute offering %q not found in catalog", computeOfferingID)
	}
}

func TestQueuedPurchaseThenDelete(t *testing.T) {
	c, clock := newClient(t)
	ctx := context.Background()

	future := clock.Now().Add(24 * time.Hour)

	out, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("0.500"),
		PurchaseTime:          aws.Time(future),
	})
	if err != nil {
		t.Fatalf("CreateSavingsPlan (future): %v", err)
	}

	planID := aws.ToString(out.SavingsPlanId)

	desc, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{
		SavingsPlanIds: []string{planID},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlans: %v", err)
	}

	if len(desc.SavingsPlans) != 1 || desc.SavingsPlans[0].State != sptypes.SavingsPlanStateQueued {
		t.Fatalf("expected a queued plan, got %+v", desc.SavingsPlans)
	}

	if _, err := c.DeleteQueuedSavingsPlan(ctx, &savingsplans.DeleteQueuedSavingsPlanInput{
		SavingsPlanId: aws.String(planID),
	}); err != nil {
		t.Fatalf("DeleteQueuedSavingsPlan: %v", err)
	}

	after, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{
		SavingsPlanIds: []string{planID},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlans (after delete): %v", err)
	}

	if len(after.SavingsPlans) != 1 || after.SavingsPlans[0].State != sptypes.SavingsPlanStateQueuedDeleted {
		t.Fatalf("expected queued-deleted, got %+v", after.SavingsPlans)
	}

	// Deleting a plan that is not queued must fail.
	if _, err := c.DeleteQueuedSavingsPlan(ctx, &savingsplans.DeleteQueuedSavingsPlanInput{
		SavingsPlanId: aws.String(planID),
	}); err == nil {
		t.Fatal("expected error deleting an already queued-deleted plan")
	}
}

func TestTagsRoundTrip(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	out, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("2.000"),
	})
	if err != nil {
		t.Fatalf("CreateSavingsPlan: %v", err)
	}

	desc, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{
		SavingsPlanIds: []string{aws.ToString(out.SavingsPlanId)},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlans: %v", err)
	}

	arn := aws.ToString(desc.SavingsPlans[0].SavingsPlanArn)

	if _, err := c.TagResource(ctx, &savingsplans.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"env": "prod", "team": "cost"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &savingsplans.ListTagsForResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if tags.Tags["env"] != "prod" || tags.Tags["team"] != "cost" {
		t.Fatalf("tags not stored: %v", tags.Tags)
	}

	if _, err := c.UntagResource(ctx, &savingsplans.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	after, err := c.ListTagsForResource(ctx, &savingsplans.ListTagsForResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource (after untag): %v", err)
	}

	if _, ok := after.Tags["env"]; ok {
		t.Fatalf("env tag should have been removed: %v", after.Tags)
	}

	if after.Tags["team"] != "cost" {
		t.Fatalf("team tag should survive untag: %v", after.Tags)
	}
}

package savingsplans_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
)

// TestCreateSavingsPlanRejectsNonDecimalCommitment: commitment is a plain
// decimal amount; exponent, hex-float and signed forms that strconv.ParseFloat
// would accept are ValidationException, and a plain decimal is stored verbatim.
func TestCreateSavingsPlanRejectsNonDecimalCommitment(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	for _, commitment := range []string{"1e2", "0x1p3", "+5", "1.23456e2", " 1", "1."} {
		_, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
			SavingsPlanOfferingId: aws.String(computeOfferingID),
			Commitment:            aws.String(commitment),
		})
		wantValidation(t, "commitment "+commitment, err)
	}

	out, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("123.456"),
	})
	if err != nil {
		t.Fatalf("CreateSavingsPlan(123.456): %v", err)
	}

	desc, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{
		SavingsPlanIds: []string{aws.ToString(out.SavingsPlanId)},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlans: %v", err)
	}

	if got := aws.ToString(desc.SavingsPlans[0].Commitment); got != "123.456" {
		t.Fatalf("stored commitment = %q, want 123.456", got)
	}
}

// TestDescribeSavingsPlansRejectsZeroMaxResults: DescribeSavingsPlans documents
// maxResults 1-1000, while DescribeSavingsPlansOfferings documents 0-1000.
func TestDescribeSavingsPlansRejectsZeroMaxResults(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	_, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{MaxResults: aws.Int32(0)})
	wantValidation(t, "DescribeSavingsPlans maxResults 0", err)

	if _, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{}); err != nil {
		t.Fatalf("DescribeSavingsPlans without maxResults: %v", err)
	}

	out, err := c.DescribeSavingsPlansOfferings(ctx, &savingsplans.DescribeSavingsPlansOfferingsInput{MaxResults: 0})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings maxResults 0: %v", err)
	}

	if len(out.SearchResults) == 0 || out.NextToken != nil {
		t.Fatalf("offerings maxResults 0 = %d results, nextToken=%v", len(out.SearchResults), out.NextToken)
	}
}

// TestDescribeOfferingsRejectsUnknownFilterName: filters[].name is the closed
// SavingsPlanOfferingFilterAttribute enum (region | instanceFamily).
func TestDescribeOfferingsRejectsUnknownFilterName(t *testing.T) {
	c, _ := newClient(t)

	_, err := c.DescribeSavingsPlansOfferings(context.Background(), &savingsplans.DescribeSavingsPlansOfferingsInput{
		Filters: []sptypes.SavingsPlanOfferingFilterElement{
			{Name: "instancefamily", Values: []string{"m5"}},
		},
	})
	wantValidation(t, "unknown offering filter name", err)
}

// TestTagsRejectReservedAWSPrefix: the aws: tag key prefix is reserved for AWS
// (case-insensitive) on both TagResource and CreateSavingsPlan.
func TestTagsRejectReservedAWSPrefix(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	_, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("1"),
		Tags:                  map[string]string{"aws:team": "x"},
	})
	wantValidation(t, "create with aws: tag", err)

	out, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(computeOfferingID),
		Commitment:            aws.String("1"),
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

	for _, key := range []string{"aws:x", "AWS:x", "Aws:Cost"} {
		_, err = c.TagResource(ctx, &savingsplans.TagResourceInput{
			ResourceArn: desc.SavingsPlans[0].SavingsPlanArn,
			Tags:        map[string]string{key: "v"},
		})
		wantValidation(t, "tag key "+key, err)
	}
}

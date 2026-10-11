package savingsplans_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/aws/smithy-go"
)

func wantValidation(t *testing.T, what string, err error) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("%s: err = %v, want ValidationException", what, err)
	}
}

// TestCreateSavingsPlanRejectsInvalidCommitment: commitment must be within
// [0.001, 1000000] with at most five decimals (CreateSavingsPlan API reference).
func TestCreateSavingsPlanRejectsInvalidCommitment(t *testing.T) {
	c, _ := newClient(t)

	for _, commitment := range []string{"0", "-1", "1.123456", "1000001"} {
		_, err := c.CreateSavingsPlan(context.Background(), &savingsplans.CreateSavingsPlanInput{
			SavingsPlanOfferingId: aws.String(computeOfferingID),
			Commitment:            aws.String(commitment),
		})
		wantValidation(t, "commitment "+commitment, err)
	}
}

func TestTagResourceRejectsEmptyKey(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

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

	_, err = c.TagResource(ctx, &savingsplans.TagResourceInput{
		ResourceArn: desc.SavingsPlans[0].SavingsPlanArn,
		Tags:        map[string]string{"": "v"},
	})
	wantValidation(t, "empty tag key", err)
}

func TestDescribeOfferingsAppliesProductTypeAndFilters(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	byProduct, err := c.DescribeSavingsPlansOfferings(ctx, &savingsplans.DescribeSavingsPlansOfferingsInput{
		ProductType: sptypes.SavingsPlanProductTypeSagemaker,
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings(productType): %v", err)
	}

	if len(byProduct.SearchResults) != 1 ||
		aws.ToString(byProduct.SearchResults[0].OfferingId) != "sp-offering-sagemaker-1yr-no" {
		t.Fatalf("productType=SageMaker returned %+v", byProduct.SearchResults)
	}

	byFamily, err := c.DescribeSavingsPlansOfferings(ctx, &savingsplans.DescribeSavingsPlansOfferingsInput{
		Filters: []sptypes.SavingsPlanOfferingFilterElement{
			{Name: sptypes.SavingsPlanOfferingFilterAttributeInstanceFamily, Values: []string{"m5"}},
			{Name: sptypes.SavingsPlanOfferingFilterAttributeRegion, Values: []string{"us-east-1"}},
		},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings(filters): %v", err)
	}

	if len(byFamily.SearchResults) != 1 {
		t.Fatalf("instanceFamily=m5,region=us-east-1 returned %d offerings", len(byFamily.SearchResults))
	}

	props := map[string]string{}
	for _, p := range byFamily.SearchResults[0].Properties {
		props[string(p.Name)] = aws.ToString(p.Value)
	}

	if props["instanceFamily"] != "m5" || props["region"] != "us-east-1" {
		t.Fatalf("offering properties = %v", props)
	}

	none, err := c.DescribeSavingsPlansOfferings(ctx, &savingsplans.DescribeSavingsPlansOfferingsInput{
		Filters: []sptypes.SavingsPlanOfferingFilterElement{
			{Name: sptypes.SavingsPlanOfferingFilterAttributeInstanceFamily, Values: []string{"c5"}},
		},
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings(c5): %v", err)
	}

	if len(none.SearchResults) != 0 {
		t.Fatalf("instanceFamily=c5 should match nothing, got %d", len(none.SearchResults))
	}
}

func TestDescribeSavingsPlansPagination(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	for range 3 {
		if _, err := c.CreateSavingsPlan(ctx, &savingsplans.CreateSavingsPlanInput{
			SavingsPlanOfferingId: aws.String(computeOfferingID),
			Commitment:            aws.String("1"),
		}); err != nil {
			t.Fatalf("CreateSavingsPlan: %v", err)
		}
	}

	seen := map[string]bool{}
	pages := 0

	var token *string

	for {
		out, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{
			MaxResults: aws.Int32(2),
			NextToken:  token,
		})
		if err != nil {
			t.Fatalf("DescribeSavingsPlans page %d: %v", pages, err)
		}

		pages++

		if len(out.SavingsPlans) > 2 {
			t.Fatalf("page %d has %d plans, want <= 2", pages, len(out.SavingsPlans))
		}

		for _, p := range out.SavingsPlans {
			seen[aws.ToString(p.SavingsPlanId)] = true
		}

		if out.NextToken == nil {
			break
		}

		token = out.NextToken
	}

	if pages != 2 || len(seen) != 3 {
		t.Fatalf("paginated %d pages / %d distinct plans, want 2 / 3", pages, len(seen))
	}

	_, err := c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{NextToken: aws.String("garbage")})
	wantValidation(t, "bad nextToken", err)

	_, err = c.DescribeSavingsPlans(ctx, &savingsplans.DescribeSavingsPlansInput{MaxResults: aws.Int32(1001)})
	wantValidation(t, "maxResults 1001", err)
}

func TestDescribeOfferingsPagination(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	first, err := c.DescribeSavingsPlansOfferings(ctx, &savingsplans.DescribeSavingsPlansOfferingsInput{MaxResults: 3})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings: %v", err)
	}

	if len(first.SearchResults) != 3 || first.NextToken == nil {
		t.Fatalf("offerings page1 = %d results, nextToken=%v", len(first.SearchResults), first.NextToken)
	}

	second, err := c.DescribeSavingsPlansOfferings(ctx, &savingsplans.DescribeSavingsPlansOfferingsInput{
		MaxResults: 3,
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeSavingsPlansOfferings page2: %v", err)
	}

	if len(second.SearchResults) != 1 || second.NextToken != nil {
		t.Fatalf("offerings page2 = %d results, nextToken=%v", len(second.SearchResults), second.NextToken)
	}
}

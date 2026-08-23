package ssm_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// TestSDKGetParameterHistoryFields guards GetParameterHistory carrying Labels,
// Description, Tier, and LastModifiedUser (previously omitted).
func TestSDKGetParameterHistoryFields(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:        aws.String("/svc/config"),
		Value:       aws.String("v1"),
		Type:        ssmtypes.ParameterTypeString,
		Description: aws.String("service config"),
		Tier:        ssmtypes.ParameterTierStandard,
	}); err != nil {
		t.Fatalf("PutParameter(v1): %v", err)
	}

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:        aws.String("/svc/config"),
		Value:       aws.String("v2"),
		Type:        ssmtypes.ParameterTypeString,
		Description: aws.String("service config"),
		Overwrite:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("PutParameter(v2): %v", err)
	}

	if _, err := client.LabelParameterVersion(ctx, &awsssm.LabelParameterVersionInput{
		Name:   aws.String("/svc/config"),
		Labels: []string{"prod"},
	}); err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}

	hist, err := client.GetParameterHistory(ctx, &awsssm.GetParameterHistoryInput{
		Name: aws.String("/svc/config"),
	})
	if err != nil {
		t.Fatalf("GetParameterHistory: %v", err)
	}

	if len(hist.Parameters) != 2 {
		t.Fatalf("history = %d entries, want 2", len(hist.Parameters))
	}

	latest := hist.Parameters[len(hist.Parameters)-1]

	if aws.ToString(latest.Description) != "service config" {
		t.Fatalf("Description = %q, want 'service config'", aws.ToString(latest.Description))
	}

	if latest.Tier != ssmtypes.ParameterTierStandard {
		t.Fatalf("Tier = %q, want Standard", latest.Tier)
	}

	if aws.ToString(latest.LastModifiedUser) == "" {
		t.Fatal("LastModifiedUser is empty")
	}

	if len(latest.Labels) != 1 || latest.Labels[0] != "prod" {
		t.Fatalf("Labels = %v, want [prod]", latest.Labels)
	}
}

// TestSDKGetParameterHistoryPagination guards MaxResults/NextToken.
func TestSDKGetParameterHistoryPagination(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	for i, v := range []string{"a", "b", "c"} {
		in := &awsssm.PutParameterInput{
			Name:  aws.String("/p"),
			Value: aws.String(v),
			Type:  ssmtypes.ParameterTypeString,
		}
		if i > 0 {
			in.Overwrite = aws.Bool(true)
		}

		if _, err := client.PutParameter(ctx, in); err != nil {
			t.Fatalf("PutParameter(%s): %v", v, err)
		}
	}

	first, err := client.GetParameterHistory(ctx, &awsssm.GetParameterHistoryInput{
		Name:       aws.String("/p"),
		MaxResults: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("GetParameterHistory(page1): %v", err)
	}

	if len(first.Parameters) != 2 || aws.ToString(first.NextToken) == "" {
		t.Fatalf("page1 = %d entries token=%q, want 2 with a token", len(first.Parameters), aws.ToString(first.NextToken))
	}

	second, err := client.GetParameterHistory(ctx, &awsssm.GetParameterHistoryInput{
		Name:      aws.String("/p"),
		NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("GetParameterHistory(page2): %v", err)
	}

	if len(second.Parameters) != 1 {
		t.Fatalf("page2 = %d entries, want 1", len(second.Parameters))
	}
}

// TestSDKDescribeParametersFilter guards ParameterFilters (BeginsWith on Name)
// actually narrowing the result set.
func TestSDKDescribeParametersFilter(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	for _, n := range []string{"/deep/one", "/deep/two", "/other/x"} {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name:  aws.String(n),
			Value: aws.String("v"),
			Type:  ssmtypes.ParameterTypeString,
		}); err != nil {
			t.Fatalf("PutParameter(%s): %v", n, err)
		}
	}

	out, err := client.DescribeParameters(ctx, &awsssm.DescribeParametersInput{
		ParameterFilters: []ssmtypes.ParameterStringFilter{{
			Key:    aws.String("Name"),
			Option: aws.String("BeginsWith"),
			Values: []string{"/deep"},
		}},
	})
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	if len(out.Parameters) != 2 {
		t.Fatalf("filtered = %d params, want 2 (the /deep/* ones)", len(out.Parameters))
	}

	for _, p := range out.Parameters {
		if got := aws.ToString(p.Name); got != "/deep/one" && got != "/deep/two" {
			t.Fatalf("unexpected parameter %q in BeginsWith /deep result", got)
		}
	}
}

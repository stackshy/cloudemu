package ssm_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// TestSDKGetParametersByPathPagination covers #266: GetParametersByPath now
// honors MaxResults/NextToken instead of returning everything at once.
func TestSDKGetParametersByPathPagination(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	for _, n := range []string{"/pg/a", "/pg/b", "/pg/c", "/pg/d", "/pg/e"} {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name: aws.String(n), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
		}); err != nil {
			t.Fatalf("PutParameter %s: %v", n, err)
		}
	}

	// First page: MaxResults=2 yields exactly 2 items and a continuation token.
	p1, err := client.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path: aws.String("/pg/"), MaxResults: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1.Parameters) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(p1.Parameters))
	}
	if aws.ToString(p1.NextToken) == "" {
		t.Fatal("page1 missing NextToken with more results available")
	}

	// The SDK paginator walks every page; assert it collects all 5 in stable
	// sorted order with no duplicates or drops.
	pager := awsssm.NewGetParametersByPathPaginator(client, &awsssm.GetParametersByPathInput{
		Path: aws.String("/pg/"), MaxResults: aws.Int32(2),
	})
	var names []string
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("paginator NextPage: %v", err)
		}
		for _, p := range page.Parameters {
			names = append(names, aws.ToString(p.Name))
		}
	}
	want := []string{"/pg/a", "/pg/b", "/pg/c", "/pg/d", "/pg/e"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("paginated names = %v, want %v", names, want)
	}
}

// TestSDKDescribeParametersPagination covers #266: DescribeParameters now
// paginates too.
func TestSDKDescribeParametersPagination(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	for _, n := range []string{"/d/1", "/d/2", "/d/3"} {
		if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
			Name: aws.String(n), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
		}); err != nil {
			t.Fatalf("PutParameter %s: %v", n, err)
		}
	}

	p1, err := client.DescribeParameters(ctx, &awsssm.DescribeParametersInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("describe page1: %v", err)
	}
	if len(p1.Parameters) != 2 || aws.ToString(p1.NextToken) == "" {
		t.Fatalf("page1 = %d params, token=%q; want 2 + token", len(p1.Parameters), aws.ToString(p1.NextToken))
	}

	p2, err := client.DescribeParameters(ctx, &awsssm.DescribeParametersInput{NextToken: p1.NextToken})
	if err != nil {
		t.Fatalf("describe page2: %v", err)
	}
	if len(p2.Parameters) != 1 {
		t.Fatalf("page2 len = %d, want 1", len(p2.Parameters))
	}
	if aws.ToString(p2.NextToken) != "" {
		t.Fatalf("page2 NextToken = %q, want empty (last page)", aws.ToString(p2.NextToken))
	}
}

// TestSDKGetParameterVersionNotFound covers #266: a missing *version* of an
// existing parameter returns the distinct ParameterVersionNotFound, while a
// missing parameter still returns ParameterNotFound.
func TestSDKGetParameterVersionNotFound(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/v/p"), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	_, err := client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/v/p:99")})
	var vnf *ssmtypes.ParameterVersionNotFound
	if !errors.As(err, &vnf) {
		t.Fatalf("missing version error = %v, want *ParameterVersionNotFound", err)
	}

	_, err = client.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/v/missing")})
	var pnf *ssmtypes.ParameterNotFound
	if !errors.As(err, &pnf) {
		t.Fatalf("missing parameter error = %v, want *ParameterNotFound", err)
	}
}

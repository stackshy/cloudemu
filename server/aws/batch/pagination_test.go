package batch_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbatch "github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/smithy-go"
)

func createUnmanagedCE(t *testing.T, c *awsbatch.Client, name string) {
	t.Helper()

	_, err := c.CreateComputeEnvironment(context.Background(), &awsbatch.CreateComputeEnvironmentInput{
		ComputeEnvironmentName: aws.String(name),
		Type:                   batchtypes.CETypeUnmanaged,
	})
	if err != nil {
		t.Fatalf("CreateComputeEnvironment %s: %v", name, err)
	}
}

func registerJD(t *testing.T, c *awsbatch.Client, name string) {
	t.Helper()

	_, err := c.RegisterJobDefinition(context.Background(), &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName: aws.String(name),
		Type:              batchtypes.JobDefinitionTypeContainer,
		ContainerProperties: &batchtypes.ContainerProperties{
			Image: aws.String("busybox"),
			ResourceRequirements: []batchtypes.ResourceRequirement{
				{Type: batchtypes.ResourceTypeVcpu, Value: aws.String("1")},
				{Type: batchtypes.ResourceTypeMemory, Value: aws.String("512")},
			},
		},
	})
	if err != nil {
		t.Fatalf("RegisterJobDefinition %s: %v", name, err)
	}
}

func jdKeys(defs []batchtypes.JobDefinition) string {
	keys := make([]string, 0, len(defs))
	for i := range defs {
		keys = append(keys, fmt.Sprintf("%s:%d", aws.ToString(defs[i].JobDefinitionName), aws.ToInt32(defs[i].Revision)))
	}

	return strings.Join(keys, ",")
}

func TestSDKDescribeComputeEnvironmentsPaging(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	for _, n := range []string{"ce-a", "ce-b", "ce-c"} {
		createUnmanagedCE(t, c, n)
	}

	p1, err := c.DescribeComputeEnvironments(ctx, &awsbatch.DescribeComputeEnvironmentsInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}

	if len(p1.ComputeEnvironments) != 2 || p1.NextToken == nil {
		t.Fatalf("page 1: want 2 items and a token, got %d items, token %v", len(p1.ComputeEnvironments), p1.NextToken)
	}

	p2, err := c.DescribeComputeEnvironments(ctx, &awsbatch.DescribeComputeEnvironmentsInput{
		MaxResults: aws.Int32(2), NextToken: p1.NextToken,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	if len(p2.ComputeEnvironments) != 1 || p2.NextToken != nil {
		t.Fatalf("page 2: want 1 item and no token, got %d items, token %v", len(p2.ComputeEnvironments), p2.NextToken)
	}

	got := []string{
		aws.ToString(p1.ComputeEnvironments[0].ComputeEnvironmentName),
		aws.ToString(p1.ComputeEnvironments[1].ComputeEnvironmentName),
		aws.ToString(p2.ComputeEnvironments[0].ComputeEnvironmentName),
	}
	if strings.Join(got, ",") != "ce-a,ce-b,ce-c" {
		t.Fatalf("pages out of order or overlapping: %v", got)
	}

	// The SDK paginator must follow the tokens and stop.
	pager := awsbatch.NewDescribeComputeEnvironmentsPaginator(c, &awsbatch.DescribeComputeEnvironmentsInput{MaxResults: aws.Int32(1)})

	pages, total := 0, 0

	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("paginator: %v", perr)
		}

		pages++
		total += len(out.ComputeEnvironments)
	}

	if pages != 3 || total != 3 {
		t.Fatalf("paginator: want 3 pages of 1, got %d pages, %d items", pages, total)
	}
}

func TestSDKDescribeJobQueuesPaging(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	createUnmanagedCE(t, c, "ce-q")

	for _, n := range []string{"q1", "q2", "q3"} {
		_, err := c.CreateJobQueue(ctx, &awsbatch.CreateJobQueueInput{
			JobQueueName: aws.String(n),
			Priority:     aws.Int32(1),
			ComputeEnvironmentOrder: []batchtypes.ComputeEnvironmentOrder{
				{Order: aws.Int32(1), ComputeEnvironment: aws.String("ce-q")},
			},
		})
		if err != nil {
			t.Fatalf("CreateJobQueue %s: %v", n, err)
		}
	}

	p1, err := c.DescribeJobQueues(ctx, &awsbatch.DescribeJobQueuesInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}

	if len(p1.JobQueues) != 2 || p1.NextToken == nil {
		t.Fatalf("page 1: want 2 items and a token, got %d items, token %v", len(p1.JobQueues), p1.NextToken)
	}

	p2, err := c.DescribeJobQueues(ctx, &awsbatch.DescribeJobQueuesInput{MaxResults: aws.Int32(2), NextToken: p1.NextToken})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	if len(p2.JobQueues) != 1 || aws.ToString(p2.JobQueues[0].JobQueueName) != "q3" || p2.NextToken != nil {
		t.Fatalf("page 2: want only q3 and no token, got %+v token %v", p2.JobQueues, p2.NextToken)
	}
}

func TestSDKDescribeJobDefinitionsPagingWithFilters(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	registerJD(t, c, "jd-x")
	registerJD(t, c, "jd-x")
	registerJD(t, c, "jd-y")

	if _, err := c.DeregisterJobDefinition(ctx, &awsbatch.DeregisterJobDefinitionInput{JobDefinition: aws.String("jd-x:1")}); err != nil {
		t.Fatalf("DeregisterJobDefinition: %v", err)
	}

	p1, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{
		Status: aws.String("ACTIVE"), MaxResults: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}

	if jdKeys(p1.JobDefinitions) != "jd-x:2" || p1.NextToken == nil {
		t.Fatalf("page 1: want jd-x:2 plus a token, got %q token %v", jdKeys(p1.JobDefinitions), p1.NextToken)
	}

	p2, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{
		Status: aws.String("ACTIVE"), MaxResults: aws.Int32(1), NextToken: p1.NextToken,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	if jdKeys(p2.JobDefinitions) != "jd-y:1" || p2.NextToken != nil {
		t.Fatalf("page 2: want jd-y:1 and no token, got %q token %v", jdKeys(p2.JobDefinitions), p2.NextToken)
	}

	byName, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{
		JobDefinitionName: aws.String("jd-x"), Status: aws.String("INACTIVE"), MaxResults: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("by name: %v", err)
	}

	if jdKeys(byName.JobDefinitions) != "jd-x:1" || byName.NextToken != nil {
		t.Fatalf("by name INACTIVE: want jd-x:1 and no token, got %q token %v", jdKeys(byName.JobDefinitions), byName.NextToken)
	}
}

// Without maxResults the API still caps a page at 100 and returns a token.
func TestSDKDescribeJobDefinitionsDefaultPageSize(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	for i := range 101 {
		registerJD(t, c, fmt.Sprintf("jd-%03d", i))
	}

	p1, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}

	if len(p1.JobDefinitions) != 100 || p1.NextToken == nil {
		t.Fatalf("page 1: want 100 items and a token, got %d, token %v", len(p1.JobDefinitions), p1.NextToken)
	}

	p2, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{NextToken: p1.NextToken})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	if jdKeys(p2.JobDefinitions) != "jd-100:1" || p2.NextToken != nil {
		t.Fatalf("page 2: want jd-100:1 and no token, got %q token %v", jdKeys(p2.JobDefinitions), p2.NextToken)
	}
}

func TestSDKDescribeRejectsBadPaging(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	ops := map[string]func(limit *int32, token *string) error{
		"DescribeComputeEnvironments": func(limit *int32, token *string) error {
			_, err := c.DescribeComputeEnvironments(ctx, &awsbatch.DescribeComputeEnvironmentsInput{MaxResults: limit, NextToken: token})
			return err
		},
		"DescribeJobQueues": func(limit *int32, token *string) error {
			_, err := c.DescribeJobQueues(ctx, &awsbatch.DescribeJobQueuesInput{MaxResults: limit, NextToken: token})
			return err
		},
		"DescribeJobDefinitions": func(limit *int32, token *string) error {
			_, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{MaxResults: limit, NextToken: token})
			return err
		},
	}

	cases := map[string]struct {
		limit *int32
		token *string
	}{
		"maxResults 0":   {limit: aws.Int32(0)},
		"maxResults 101": {limit: aws.Int32(101)},
		"bogus token":    {token: aws.String("not-a-token")},
	}

	for op, call := range ops {
		for name, tc := range cases {
			err := call(tc.limit, tc.token)

			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ClientException" {
				t.Errorf("%s %s: want ClientException, got %v", op, name, err)
			}
		}
	}
}

// uuid is a real ComputeEnvironmentDetail member. Real Batch reuses it as the
// suffix of the managed ECS cluster name.
func TestSDKComputeEnvironmentUUID(t *testing.T) {
	c := newBatchClient(t)
	createFargateCE(t, c, "ce-uuid")

	out, err := c.DescribeComputeEnvironments(context.Background(), &awsbatch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []string{"ce-uuid"},
	})
	if err != nil {
		t.Fatalf("DescribeComputeEnvironments: %v", err)
	}

	ce := out.ComputeEnvironments[0]

	uuid := aws.ToString(ce.Uuid)
	if uuid == "" || !strings.HasSuffix(aws.ToString(ce.EcsClusterArn), "_Batch_"+uuid) {
		t.Fatalf("want uuid set and used as the ecsClusterArn suffix, got uuid %q arn %q", uuid, aws.ToString(ce.EcsClusterArn))
	}
}

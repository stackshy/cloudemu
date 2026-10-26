package batch_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbatch "github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
)

func TestSDKListJobsPaginatorAndFilters(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)

	for _, n := range []string{"alpha", "beta", "gamma"} {
		if _, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
			JobName: aws.String(n), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"),
		}); err != nil {
			t.Fatalf("SubmitJob: %v", err)
		}
	}

	p := awsbatch.NewListJobsPaginator(c, &awsbatch.ListJobsInput{
		JobQueue: aws.String("q"), JobStatus: batchtypes.JobStatusSucceeded, MaxResults: aws.Int32(2),
	})

	var names []string

	pages := 0

	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		for _, s := range page.JobSummaryList {
			names = append(names, aws.ToString(s.JobName))
		}
	}

	if pages != 2 || len(names) != 3 || names[0] != "gamma" || names[2] != "alpha" {
		t.Fatalf("paginated ListJobs = %v over %d pages, want [gamma beta alpha] over 2", names, pages)
	}

	byName, err := c.ListJobs(ctx, &awsbatch.ListJobsInput{
		JobQueue: aws.String("q"),
		Filters:  []batchtypes.KeyValuesPair{{Name: aws.String("JOB_NAME"), Values: []string{"GAM*"}}},
	})
	if err != nil || len(byName.JobSummaryList) != 1 || aws.ToString(byName.JobSummaryList[0].JobName) != "gamma" {
		t.Fatalf("JOB_NAME filter: %+v, %v", byName, err)
	}

	_, err = c.ListJobs(ctx, &awsbatch.ListJobsInput{JobQueue: aws.String("q"), NextToken: aws.String("!!")})
	requireClientException(t, err, "ListJobs bad nextToken")

	_, err = c.ListJobs(ctx, &awsbatch.ListJobsInput{})
	requireClientException(t, err, "ListJobs without jobQueue/arrayJobId/multiNodeJobId")
}

func TestSDKReservedTagPrefixRejected(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)

	_, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("j"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"),
		Tags: map[string]string{"aws:team": "x"},
	})
	requireClientException(t, err, "SubmitJob with aws: tag")

	sub, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("j"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"),
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	_, err = c.TagResource(ctx, &awsbatch.TagResourceInput{ResourceArn: sub.JobArn, Tags: map[string]string{"AWS:x": "y"}})
	requireClientException(t, err, "TagResource with aws: tag")

	if _, err := c.TagResource(ctx, &awsbatch.TagResourceInput{
		ResourceArn: sub.JobArn, Tags: map[string]string{"team": "a"},
	}); err != nil {
		t.Fatalf("TagResource(job): %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsbatch.ListTagsForResourceInput{ResourceArn: sub.JobArn})
	if err != nil || tags.Tags["team"] != "a" {
		t.Fatalf("ListTagsForResource(job): %+v, %v", tags, err)
	}
}

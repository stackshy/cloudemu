package sfn_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
)

// TestSDKListActivitiesPagination walks ListActivities across pages over three
// activities: maxResults=2 yields a full page with a token then a final page
// without one, each activity ARN once.
func TestSDKListActivitiesPagination(t *testing.T) {
	client := newSFNClient(t)
	ctx := context.Background()

	for _, name := range []string{"act1", "act2", "act3"} {
		if _, err := client.CreateActivity(ctx, &awssfn.CreateActivityInput{Name: aws.String(name)}); err != nil {
			t.Fatalf("CreateActivity(%s): %v", name, err)
		}
	}

	page1, err := client.ListActivities(ctx, &awssfn.ListActivitiesInput{MaxResults: 2})
	if err != nil {
		t.Fatalf("ListActivities page1: %v", err)
	}

	if len(page1.Activities) != 2 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d activities token=%q, want 2 with token",
			len(page1.Activities), aws.ToString(page1.NextToken))
	}

	page2, err := client.ListActivities(ctx, &awssfn.ListActivitiesInput{
		MaxResults: 2, NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListActivities page2: %v", err)
	}

	if len(page2.Activities) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d activities token=%q, want 1 no token",
			len(page2.Activities), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, a := range append(page1.Activities, page2.Activities...) {
		arn := aws.ToString(a.ActivityArn)
		if seen[arn] {
			t.Fatalf("activity %q returned twice across pages", arn)
		}

		seen[arn] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique activities, want 3", len(seen))
	}

	all, err := client.ListActivities(ctx, &awssfn.ListActivitiesInput{})
	if err != nil {
		t.Fatalf("ListActivities all: %v", err)
	}

	if len(all.Activities) != 3 || aws.ToString(all.NextToken) != "" {
		t.Fatalf("single page = %d activities token=%q, want 3 no token",
			len(all.Activities), aws.ToString(all.NextToken))
	}
}

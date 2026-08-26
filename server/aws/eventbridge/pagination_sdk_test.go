package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// TestSDKListEventBusesPagination walks ListEventBuses across pages over three
// buses (a NamePrefix filter excludes the always-present default bus), asserting
// Limit caps the page, the token resumes, and each bus appears once.
func TestSDKListEventBusesPagination(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	for _, name := range []string{"bus1", "bus2", "bus3"} {
		if _, err := client.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String(name)}); err != nil {
			t.Fatalf("CreateEventBus(%s): %v", name, err)
		}
	}

	page1, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{
		NamePrefix: aws.String("bus"), Limit: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListEventBuses page1: %v", err)
	}

	if len(page1.EventBuses) != 2 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d buses token=%q, want 2 with token", len(page1.EventBuses), aws.ToString(page1.NextToken))
	}

	page2, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{
		NamePrefix: aws.String("bus"), Limit: aws.Int32(2), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListEventBuses page2: %v", err)
	}

	if len(page2.EventBuses) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d buses token=%q, want 1 no token", len(page2.EventBuses), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, b := range append(page1.EventBuses, page2.EventBuses...) {
		name := aws.ToString(b.Name)
		if seen[name] {
			t.Fatalf("bus %q returned twice across pages", name)
		}

		seen[name] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique buses, want 3", len(seen))
	}

	all, err := client.ListEventBuses(ctx, &awseb.ListEventBusesInput{NamePrefix: aws.String("bus")})
	if err != nil {
		t.Fatalf("ListEventBuses all: %v", err)
	}

	if len(all.EventBuses) != 3 || aws.ToString(all.NextToken) != "" {
		t.Fatalf("single page = %d buses token=%q, want 3 no token", len(all.EventBuses), aws.ToString(all.NextToken))
	}
}

// TestSDKListTargetsByRulePagination walks ListTargetsByRule across pages over
// three targets on one rule.
func TestSDKListTargetsByRulePagination(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("rule"), EventPattern: aws.String(`{"source":["app"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	targets := []ebtypes.Target{
		{Id: aws.String("t1"), Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:q1")},
		{Id: aws.String("t2"), Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:q2")},
		{Id: aws.String("t3"), Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:q3")},
	}
	if _, err := client.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("rule"), Targets: targets,
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	page1, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{
		Rule: aws.String("rule"), Limit: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListTargetsByRule page1: %v", err)
	}

	if len(page1.Targets) != 2 || aws.ToString(page1.NextToken) == "" {
		t.Fatalf("page1 = %d targets token=%q, want 2 with token", len(page1.Targets), aws.ToString(page1.NextToken))
	}

	page2, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{
		Rule: aws.String("rule"), Limit: aws.Int32(2), NextToken: page1.NextToken,
	})
	if err != nil {
		t.Fatalf("ListTargetsByRule page2: %v", err)
	}

	if len(page2.Targets) != 1 || aws.ToString(page2.NextToken) != "" {
		t.Fatalf("page2 = %d targets token=%q, want 1 no token", len(page2.Targets), aws.ToString(page2.NextToken))
	}

	seen := map[string]bool{}
	for _, tg := range append(page1.Targets, page2.Targets...) {
		id := aws.ToString(tg.Id)
		if seen[id] {
			t.Fatalf("target %q returned twice across pages", id)
		}

		seen[id] = true
	}

	if len(seen) != 3 {
		t.Fatalf("walked %d unique targets, want 3", len(seen))
	}

	all, err := client.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{Rule: aws.String("rule")})
	if err != nil {
		t.Fatalf("ListTargetsByRule all: %v", err)
	}

	if len(all.Targets) != 3 || aws.ToString(all.NextToken) != "" {
		t.Fatalf("single page = %d targets token=%q, want 3 no token", len(all.Targets), aws.ToString(all.NextToken))
	}
}

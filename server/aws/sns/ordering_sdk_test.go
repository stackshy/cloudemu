package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: ListTopics must return the same TopicArn sequence on every call. The
// pre-fix bug was random map iteration order, so repetition is the signal.
func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	names := []string{"zeta", "alpha", "mid", "beta", "omega"}
	for _, name := range names {
		if _, err := client.CreateTopic(ctx, &awssns.CreateTopicInput{
			Name: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateTopic(%s): %v", name, err)
		}
	}

	list := func() []string {
		out, err := client.ListTopics(ctx, &awssns.ListTopicsInput{})
		if err != nil {
			t.Fatalf("ListTopics: %v", err)
		}

		got := make([]string, 0, len(out.Topics))
		for i := range out.Topics {
			got = append(got, aws.ToString(out.Topics[i].TopicArn))
		}

		return got
	}

	first := list()
	if len(first) != len(names) {
		t.Fatalf("ListTopics returned %d topics, want %d: %v", len(first), len(names), first)
	}

	for call := 2; call <= 5; call++ {
		got := list()
		if len(got) != len(first) {
			t.Fatalf("call %d: got %d topics, want %d", call, len(got), len(first))
		}

		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("call %d: order diverged at index %d: got %v, first call %v", call, i, got, first)
			}
		}
	}
}

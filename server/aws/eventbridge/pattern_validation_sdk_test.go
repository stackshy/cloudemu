package eventbridge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/smithy-go"
)

// TestSDKEventBridgePutRuleInvalidPattern verifies PutRule rejects a structurally
// invalid event pattern with InvalidEventPatternException instead of storing a
// pattern that silently matches nothing.
func TestSDKEventBridgePutRuleInvalidPattern(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		pattern string
	}{
		{"non-array leaf", `{"source":"not-an-array"}`},
		{"malformed json", `{not json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.PutRule(ctx, &awseb.PutRuleInput{
				Name:         aws.String("bad-" + tc.name),
				EventPattern: aws.String(tc.pattern),
			})
			if err == nil {
				t.Fatalf("PutRule(%s) should fail, got nil", tc.pattern)
			}

			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidEventPatternException" {
				t.Fatalf("want InvalidEventPatternException, got %T: %v", err, err)
			}
		})
	}
}

// TestSDKEventBridgePutRuleValidPatternAccepted verifies patterns using arrays,
// content operators, and nested detail still pass validation.
func TestSDKEventBridgePutRuleValidPatternAccepted(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	valid := []string{
		`{"source":["aws.ec2"]}`,
		`{"source":[{"prefix":"aws."}]}`,
		`{"detail":{"state":["running"]},"detail-type":["EC2 Instance State-change Notification"]}`,
	}

	for i, pattern := range valid {
		if _, err := client.PutRule(ctx, &awseb.PutRuleInput{
			Name:         aws.String("good-" + string(rune('a'+i))),
			EventPattern: aws.String(pattern),
		}); err != nil {
			t.Fatalf("PutRule(%s) should succeed, got %v", pattern, err)
		}
	}
}

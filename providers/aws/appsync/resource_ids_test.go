package appsync_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

func TestAPIKeyIDShape(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	re := regexp.MustCompile(`^da2-[a-z0-9]{26}$`)
	seen := map[string]bool{}

	for i := 0; i < 3; i++ {
		key, err := m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID})
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}

		if !re.MatchString(key.ID) {
			t.Fatalf("api key id %q does not match %s", key.ID, re)
		}

		if seen[key.ID] {
			t.Fatalf("duplicate api key id %q", key.ID)
		}

		seen[key.ID] = true
	}
}

// TestGraphqlAPIARNPassesTagResourceValidation guards the ARN the CLI sends to
// tag-resource. botocore checks ResourceArn against the model's length (70-75)
// and pattern before the request leaves the client.
func TestGraphqlAPIARNPassesTagResourceValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	re := regexp.MustCompile(`^arn:aws:appsync:[A-Za-z0-9_/.-]{0,63}:\d{12}:apis/[0-9A-Za-z_-]{26}$`)
	if !re.MatchString(api.ARN) {
		t.Fatalf("arn %q does not match the ResourceArn pattern", api.ARN)
	}

	if n := len(api.ARN); n < 70 || n > 75 {
		t.Fatalf("arn %q has length %d, want 70-75", api.ARN, n)
	}

	if err := m.TagResource(ctx, api.ARN, map[string]string{"env": "test"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
}

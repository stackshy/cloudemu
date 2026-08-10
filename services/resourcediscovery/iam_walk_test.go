package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/iam"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestWalkIAM(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	i := iam.New(opts)

	if _, err := i.CreateUser(ctx, iamdriver.UserConfig{Name: "alice"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{IAM: i})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for j := range res {
		if res[j].Service == ServiceIAM && res[j].Type == TypeUser {
			found++
			if res[j].ID != "alice" {
				t.Fatalf("user ID = %q, want alice", res[j].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered IAM user, got %d (of %d resources)", found, len(res))
	}
}

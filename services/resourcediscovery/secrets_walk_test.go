package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/secretsmanager"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func TestWalkSecrets(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	sm := secretsmanager.New(opts)

	if _, err := sm.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "db-password"}, []byte("s3cr3t")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Secrets: sm})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceSecrets && res[i].Type == TypeSecret {
			found++
			if res[i].ID != "db-password" {
				t.Fatalf("secret ID = %q, want db-password", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered secret, got %d (of %d resources)", found, len(res))
	}
}

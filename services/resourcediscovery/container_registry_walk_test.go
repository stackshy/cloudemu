package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ecr"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func TestWalkContainerRegistry(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	reg := ecr.New(opts)

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "web-app"}); err != nil {
		t.Fatalf("create repository: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{ContainerReg: reg})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceContainer && res[i].Type == TypeRepository {
			found++
			if res[i].ID != "web-app" {
				t.Fatalf("repository ID = %q, want web-app", res[i].ID)
			}
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered repository, got %d (of %d resources)", found, len(res))
	}
}

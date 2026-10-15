package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	gcpiam "github.com/stackshy/cloudemu/v2/providers/gcp/iam"
	gcppubsub "github.com/stackshy/cloudemu/v2/providers/gcp/pubsub"
	gcpsecrets "github.com/stackshy/cloudemu/v2/providers/gcp/secretmanager"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestDiscoverySeesAllProjects pins that the inventory (which also backs
// Cloud Asset and cost) spans every GCP project, while each project-scoped
// driver list on its own covers one project.
func TestDiscoverySeesAllProjects(t *testing.T) {
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithProjectID("p-a"))
	sm := gcpsecrets.New(opts)
	ps := gcppubsub.New(opts)
	iam := gcpiam.New(opts)

	for _, p := range []string{"p-a", "p-b"} {
		ctx := projectctx.WithProject(context.Background(), p)

		if _, err := sm.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "sec1"}, nil); err != nil {
			t.Fatalf("create secret in %s: %v", p, err)
		}

		if _, err := ps.CreateQueue(ctx, mqdriver.QueueConfig{Name: "t1"}); err != nil {
			t.Fatalf("create topic in %s: %v", p, err)
		}

		if _, err := iam.CreateRole(ctx, iamdriver.RoleConfig{Name: "r1"}); err != nil {
			t.Fatalf("create role in %s: %v", p, err)
		}
	}

	eng := New(ProviderGCP, "p-a", "us-central1", &Drivers{Secrets: sm, MessageQueue: ps, IAM: iam})

	res, err := eng.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	counts := map[string]int{}
	for i := range res {
		counts[res[i].Service]++
	}

	for _, svc := range []string{ServiceSecrets, ServiceQueue, ServiceIAM} {
		if counts[svc] < 2 {
			t.Fatalf("service %s discovered %d resources, want both projects (all: %v)", svc, counts[svc], counts)
		}
	}

	if list, _ := sm.ListSecrets(context.Background()); len(list) != 1 {
		t.Fatalf("unstamped ListSecrets = %d, want the default project only", len(list))
	}
}

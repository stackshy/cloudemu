package acr

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	for _, rg := range []string{"Cas1", "cas10"} {
		if _, _, err := m.CreateOrUpdateRegistry(ctx, rg, "reg", driver.AzureRegistryConfig{Location: "eastus"}); err != nil {
			t.Fatal(err)
		}

		cfg := driver.AzureWebhookConfig{Location: "eastus", ServiceURI: "https://example.com", Actions: []string{"push"}}
		if _, _, err := m.CreateOrUpdateWebhook(ctx, rg, "reg", "wh", cfg); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "cas1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetRegistry(ctx, "Cas1", "reg"); err == nil {
		t.Error("registry in Cas1 survived the purge")
	}

	if n := len(m.webhooks.All()); n != 1 {
		t.Errorf("webhooks left = %d, want 1 (the cas10 one)", n)
	}

	if _, err := m.GetRegistry(ctx, "cas10", "reg"); err != nil {
		t.Errorf("registry in cas10 was purged with cas1: %v", err)
	}
}

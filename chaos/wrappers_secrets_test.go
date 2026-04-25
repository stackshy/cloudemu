package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
)

func newChaosSecrets(t *testing.T) (secretsdriver.Secrets, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapSecrets(cloudemu.NewAWS().SecretsManager, e), e
}

func TestWrapSecretsCreateSecretChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()

	if _, err := s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "ok"}, []byte("v")); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if _, err := s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "fail"}, []byte("v")); err == nil {
		t.Error("expected chaos error on CreateSecret")
	}
}

func TestWrapSecretsDeleteSecretChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()
	_, _ = s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "del"}, []byte("v"))

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if err := s.DeleteSecret(ctx, "del"); err == nil {
		t.Error("expected chaos error on DeleteSecret")
	}
}

func TestWrapSecretsGetSecretChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()
	_, _ = s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "g"}, []byte("v"))

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if _, err := s.GetSecret(ctx, "g"); err == nil {
		t.Error("expected chaos error on GetSecret")
	}
}

func TestWrapSecretsListSecretsChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if _, err := s.ListSecrets(ctx); err == nil {
		t.Error("expected chaos error on ListSecrets")
	}
}

func TestWrapSecretsPutSecretValueChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()
	_, _ = s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "p"}, []byte("v"))

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if _, err := s.PutSecretValue(ctx, "p", []byte("v2")); err == nil {
		t.Error("expected chaos error on PutSecretValue")
	}
}

func TestWrapSecretsGetSecretValueChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()
	_, _ = s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "gv"}, []byte("v"))

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if _, err := s.GetSecretValue(ctx, "gv", ""); err == nil {
		t.Error("expected chaos error on GetSecretValue")
	}
}

func TestWrapSecretsListSecretVersionsChaos(t *testing.T) {
	s, e := newChaosSecrets(t)
	ctx := context.Background()
	_, _ = s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "lv"}, []byte("v"))

	e.Apply(chaos.ServiceOutage("secrets", time.Hour))

	if _, err := s.ListSecretVersions(ctx, "lv"); err == nil {
		t.Error("expected chaos error on ListSecretVersions")
	}
}

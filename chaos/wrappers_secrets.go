package chaos

import (
	"context"

	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
)

// chaosSecrets wraps a secrets driver. All ops are wrapped — the surface is
// small and every call is data-plane.
type chaosSecrets struct {
	secretsdriver.Secrets
	engine *Engine
}

// WrapSecrets returns a secrets driver that consults engine on every call.
func WrapSecrets(inner secretsdriver.Secrets, engine *Engine) secretsdriver.Secrets {
	return &chaosSecrets{Secrets: inner, engine: engine}
}

func (c *chaosSecrets) CreateSecret(
	ctx context.Context, cfg secretsdriver.SecretConfig, value []byte,
) (*secretsdriver.SecretInfo, error) {
	if err := applyChaos(ctx, c.engine, "secrets", "CreateSecret"); err != nil {
		return nil, err
	}

	return c.Secrets.CreateSecret(ctx, cfg, value)
}

func (c *chaosSecrets) DeleteSecret(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "secrets", "DeleteSecret"); err != nil {
		return err
	}

	return c.Secrets.DeleteSecret(ctx, name)
}

func (c *chaosSecrets) GetSecret(ctx context.Context, name string) (*secretsdriver.SecretInfo, error) {
	if err := applyChaos(ctx, c.engine, "secrets", "GetSecret"); err != nil {
		return nil, err
	}

	return c.Secrets.GetSecret(ctx, name)
}

func (c *chaosSecrets) ListSecrets(ctx context.Context) ([]secretsdriver.SecretInfo, error) {
	if err := applyChaos(ctx, c.engine, "secrets", "ListSecrets"); err != nil {
		return nil, err
	}

	return c.Secrets.ListSecrets(ctx)
}

func (c *chaosSecrets) PutSecretValue(
	ctx context.Context, name string, value []byte,
) (*secretsdriver.SecretVersion, error) {
	if err := applyChaos(ctx, c.engine, "secrets", "PutSecretValue"); err != nil {
		return nil, err
	}

	return c.Secrets.PutSecretValue(ctx, name, value)
}

func (c *chaosSecrets) GetSecretValue(
	ctx context.Context, name, versionID string,
) (*secretsdriver.SecretVersion, error) {
	if err := applyChaos(ctx, c.engine, "secrets", "GetSecretValue"); err != nil {
		return nil, err
	}

	return c.Secrets.GetSecretValue(ctx, name, versionID)
}

func (c *chaosSecrets) ListSecretVersions(
	ctx context.Context, name string,
) ([]secretsdriver.SecretVersion, error) {
	if err := applyChaos(ctx, c.engine, "secrets", "ListSecretVersions"); err != nil {
		return nil, err
	}

	return c.Secrets.ListSecretVersions(ctx, name)
}

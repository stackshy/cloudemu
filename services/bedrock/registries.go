package bedrock

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- Inference profiles ---

// CreateInferenceProfile creates an application inference profile.
func (b *Bedrock) CreateInferenceProfile(ctx context.Context, cfg driver.InferenceProfileConfig) (*driver.InferenceProfile, error) {
	out, err := b.do(ctx, "CreateInferenceProfile", cfg.Name, func() (any, error) {
		return b.driver.CreateInferenceProfile(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.InferenceProfile), nil
}

// GetInferenceProfile retrieves an inference profile by ID or ARN.
func (b *Bedrock) GetInferenceProfile(ctx context.Context, identifier string) (*driver.InferenceProfile, error) {
	out, err := b.do(ctx, "GetInferenceProfile", identifier, func() (any, error) {
		return b.driver.GetInferenceProfile(ctx, identifier)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.InferenceProfile), nil
}

// ListInferenceProfiles lists all inference profiles.
func (b *Bedrock) ListInferenceProfiles(ctx context.Context) ([]driver.InferenceProfile, error) {
	out, err := b.do(ctx, "ListInferenceProfiles", nil, func() (any, error) { return b.driver.ListInferenceProfiles(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.InferenceProfile), nil
}

// DeleteInferenceProfile deletes an inference profile by ID or ARN.
func (b *Bedrock) DeleteInferenceProfile(ctx context.Context, identifier string) error {
	_, err := b.do(ctx, "DeleteInferenceProfile", identifier, func() (any, error) {
		return nil, b.driver.DeleteInferenceProfile(ctx, identifier)
	})

	return err
}

// --- Prompt routers ---

// CreatePromptRouter creates a prompt router.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) CreatePromptRouter(ctx context.Context, cfg driver.PromptRouterConfig) (*driver.PromptRouter, error) {
	out, err := b.do(ctx, "CreatePromptRouter", cfg.Name, func() (any, error) {
		return b.driver.CreatePromptRouter(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PromptRouter), nil
}

// GetPromptRouter retrieves a prompt router by its ARN.
func (b *Bedrock) GetPromptRouter(ctx context.Context, promptRouterARN string) (*driver.PromptRouter, error) {
	out, err := b.do(ctx, "GetPromptRouter", promptRouterARN, func() (any, error) {
		return b.driver.GetPromptRouter(ctx, promptRouterARN)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PromptRouter), nil
}

// ListPromptRouters lists all prompt routers.
func (b *Bedrock) ListPromptRouters(ctx context.Context) ([]driver.PromptRouter, error) {
	out, err := b.do(ctx, "ListPromptRouters", nil, func() (any, error) { return b.driver.ListPromptRouters(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.PromptRouter), nil
}

// DeletePromptRouter deletes a prompt router by its ARN.
func (b *Bedrock) DeletePromptRouter(ctx context.Context, promptRouterARN string) error {
	_, err := b.do(ctx, "DeletePromptRouter", promptRouterARN, func() (any, error) {
		return nil, b.driver.DeletePromptRouter(ctx, promptRouterARN)
	})

	return err
}

// --- Automated reasoning policies ---

// CreateAutomatedReasoningPolicy creates an automated reasoning policy.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) CreateAutomatedReasoningPolicy(
	ctx context.Context, cfg driver.AutomatedReasoningPolicyConfig,
) (*driver.AutomatedReasoningPolicy, error) {
	out, err := b.do(ctx, "CreateAutomatedReasoningPolicy", cfg.Name, func() (any, error) {
		return b.driver.CreateAutomatedReasoningPolicy(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AutomatedReasoningPolicy), nil
}

// GetAutomatedReasoningPolicy retrieves an automated reasoning policy by its ARN.
func (b *Bedrock) GetAutomatedReasoningPolicy(ctx context.Context, policyARN string) (*driver.AutomatedReasoningPolicy, error) {
	out, err := b.do(ctx, "GetAutomatedReasoningPolicy", policyARN, func() (any, error) {
		return b.driver.GetAutomatedReasoningPolicy(ctx, policyARN)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AutomatedReasoningPolicy), nil
}

// ListAutomatedReasoningPolicies lists all automated reasoning policies.
func (b *Bedrock) ListAutomatedReasoningPolicies(ctx context.Context) ([]driver.AutomatedReasoningPolicy, error) {
	out, err := b.do(ctx, "ListAutomatedReasoningPolicies", nil, func() (any, error) {
		return b.driver.ListAutomatedReasoningPolicies(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.AutomatedReasoningPolicy), nil
}

// UpdateAutomatedReasoningPolicy updates an automated reasoning policy.
func (b *Bedrock) UpdateAutomatedReasoningPolicy(
	ctx context.Context, policyARN string, upd driver.AutomatedReasoningPolicyUpdate,
) (*driver.AutomatedReasoningPolicy, error) {
	out, err := b.do(ctx, "UpdateAutomatedReasoningPolicy", policyARN, func() (any, error) {
		return b.driver.UpdateAutomatedReasoningPolicy(ctx, policyARN, upd)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AutomatedReasoningPolicy), nil
}

// DeleteAutomatedReasoningPolicy deletes an automated reasoning policy by its ARN.
func (b *Bedrock) DeleteAutomatedReasoningPolicy(ctx context.Context, policyARN string) error {
	_, err := b.do(ctx, "DeleteAutomatedReasoningPolicy", policyARN, func() (any, error) {
		return nil, b.driver.DeleteAutomatedReasoningPolicy(ctx, policyARN)
	})

	return err
}

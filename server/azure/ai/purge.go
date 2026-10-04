package ai

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// rgPurger is the optional resource-group purge of the Azure AI driver; the
// Azure ai.Mock implements it for both Cognitive Services and Machine Learning.
type rgPurger interface {
	PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error
}

func purge(ctx context.Context, svc any, subscription, resourceGroup string) error {
	p, ok := svc.(rgPurger)
	if !ok {
		return cerrors.Newf(cerrors.Unimplemented, "azure ai backend %T cannot purge a resource group", svc)
	}

	return p.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// PurgeResourceGroup deletes every Cognitive Services account, with its
// children, in subscription/resourceGroup, backing the resource-group cascade.
func (h *CognitiveServicesHandler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return purge(ctx, h.svc, subscription, resourceGroup)
}

// PurgeResourceGroup deletes every Machine Learning workspace and registry,
// with their children, in subscription/resourceGroup, backing the
// resource-group cascade.
func (h *MachineLearningHandler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return purge(ctx, h.svc, subscription, resourceGroup)
}

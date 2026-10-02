package ai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// PurgeResourceGroup deletes every Cognitive Services account and Machine
// Learning workspace or registry in resourceGroup, with all their children. It
// backs the ARM resource-group delete cascade. The stores key on resource
// group and name without the subscription, so one record answers for that
// group in every subscription; the purge follows the same rule and matches on
// the group alone.
func (m *Mock) PurgeResourceGroup(_ context.Context, _, resourceGroup string) error {
	if resourceGroup == "" {
		return nil
	}

	purgeGroupKeys(m.accounts, resourceGroup)
	purgeGroupKeys(m.accountKeys, resourceGroup)
	purgeGroupKeys(m.deployments, resourceGroup)
	purgeGroupKeys(m.projects, resourceGroup)
	purgeGroupKeys(m.raiPolicies, resourceGroup)
	purgeGroupKeys(m.commitmentPlans, resourceGroup)
	purgeGroupKeys(m.privateEndpoints, resourceGroup)
	purgeGroupKeys(m.mlWorkspaces, resourceGroup)
	purgeGroupKeys(m.computes, resourceGroup)
	purgeGroupKeys(m.mlEndpoints, resourceGroup)
	purgeGroupKeys(m.mlDeploys, resourceGroup)
	purgeGroupKeys(m.jobs, resourceGroup)
	purgeGroupKeys(m.assets, resourceGroup)
	purgeGroupKeys(m.datastores, resourceGroup)
	purgeGroupKeys(m.connections, resourceGroup)
	purgeGroupKeys(m.mlSchedules, resourceGroup)
	purgeGroupKeys(m.registries, resourceGroup)

	return nil
}

// purgeGroupKeys deletes every entry whose "{rg}/..." key starts with the
// resource group, compared case-insensitively.
func purgeGroupKeys[V any](store *memstore.Store[V], resourceGroup string) {
	for _, k := range store.Keys() {
		if rg, _, ok := strings.Cut(k, "/"); ok && strings.EqualFold(rg, resourceGroup) {
			store.Delete(k)
		}
	}
}

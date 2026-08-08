package azure

import (
	"context"

	aidriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// azureMLDiscovery projects the durable Azure AI resources into the
// cross-service inventory: Machine Learning workspaces and Cognitive Services
// (Azure OpenAI / AI Services) accounts. Mirrors the AWS sagemakerDiscovery and
// GCP vertexDiscovery adapters so ML is surfaced on all three providers.
type azureMLDiscovery struct{ m azureMLMock }

// azureMLMock is the subset of the Azure AI mock discovery reads.
type azureMLMock interface {
	ListMLWorkspaces(ctx context.Context) ([]aidriver.MLWorkspace, error)
	ListAccounts(ctx context.Context) ([]aidriver.Account, error)
}

func (a azureMLDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	workspaces, err := a.m.ListMLWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	accounts, err := a.m.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredResource, 0, len(workspaces)+len(accounts))

	for i := range workspaces {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceAzureML, Type: resourcediscovery.TypeWorkspace,
			ID: workspaces[i].Name, ARN: workspaces[i].ID,
			Region: workspaces[i].Location, Tags: workspaces[i].Tags,
		})
	}

	for i := range accounts {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceCognitive, Type: resourcediscovery.TypeAccount,
			ID: accounts[i].Name, ARN: accounts[i].ID,
			Region: accounts[i].Location, Tags: accounts[i].Tags,
		})
	}

	return out, nil
}

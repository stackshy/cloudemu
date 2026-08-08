package azure

import (
	"context"

	aidriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// azureMLDiscovery projects the durable Azure AI resources into the
// cross-service inventory: Machine Learning workspaces, their online endpoints,
// and Cognitive Services (Azure OpenAI / AI Services) accounts. Mirrors the AWS
// sagemakerDiscovery and GCP vertexDiscovery adapters so ML — including serving
// endpoints — is surfaced on all three providers.
type azureMLDiscovery struct{ m azureMLMock }

// azureMLMock is the subset of the Azure AI mock discovery reads.
type azureMLMock interface {
	ListMLWorkspaces(ctx context.Context) ([]aidriver.MLWorkspace, error)
	ListAccounts(ctx context.Context) ([]aidriver.Account, error)
	ListEndpoints(ctx context.Context, resourceGroup, workspace, kind string) ([]aidriver.Endpoint, error)
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

		// Online endpoints are the durable serving resource, mirroring SageMaker
		// and Vertex endpoints. They live under a workspace, so enumerate per
		// workspace and carry the workspace's region.
		endpoints, err := a.m.ListEndpoints(ctx, workspaces[i].ResourceGroup, workspaces[i].Name, "online")
		if err != nil {
			return nil, err
		}

		for j := range endpoints {
			out = append(out, resourcediscovery.DiscoveredResource{
				Service: resourcediscovery.ServiceAzureML, Type: resourcediscovery.TypeEndpoint,
				ID: endpoints[j].Name, ARN: endpoints[j].ID,
				Region: workspaces[i].Location,
			})
		}
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

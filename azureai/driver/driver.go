// Package driver defines the interfaces for Azure AI emulation, spanning the
// two ARM providers that make up "Azure AI": Microsoft.CognitiveServices (Azure
// AI Foundry / AI Studio, the AI Services resource, and Azure OpenAI) and
// Microsoft.MachineLearningServices (Azure Machine Learning). It also defines
// the data-plane surfaces: Azure OpenAI inference, the AI Foundry
// Agents/Assistants API, and AML online-endpoint scoring.
//
// The interfaces use plain Go types only (no cloud SDK dependencies). ARM
// resource IDs follow the standard
// /subscriptions/{s}/resourceGroups/{rg}/providers/{provider}/{type}/{name}
// convention. Control-plane mutations complete synchronously (ARM PUT returns
// the resource inline with a terminal provisioningState).
package driver

// Provisioning-state values shared by ARM resources across both providers.
const (
	StateSucceeded = "Succeeded"
	StateCreating  = "Creating"
	StateUpdating  = "Updating"
	StateDeleting  = "Deleting"
	StateFailed    = "Failed"
	StateCanceled  = "Canceled"
)

// AzureAI is the full Azure AI surface: both ARM providers plus the data
// planes. Implementations (the in-memory Mock) satisfy this; individual server
// handlers take the narrower per-area interface they need.
type AzureAI interface {
	CognitiveServices
	MachineLearning
	DataPlane
}

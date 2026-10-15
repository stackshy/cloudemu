package bedrockagent

import (
	"encoding/json"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// JSON wire shapes for the AWS Bedrock Agent restJson1 protocol. Field names
// use the exact camelCase keys the real aws-sdk-go-v2/service/bedrockagent
// client emits and expects. Nested configuration blocks are passed through
// verbatim as json.RawMessage.

// --- agents ---

type createAgentRequest struct {
	AgentName                   string            `json:"agentName"`
	AgentResourceRoleArn        string            `json:"agentResourceRoleArn"`
	FoundationModel             string            `json:"foundationModel"`
	Instruction                 string            `json:"instruction"`
	Description                 string            `json:"description"`
	IdleSessionTTLInSeconds     int32             `json:"idleSessionTTLInSeconds"`
	ClientToken                 string            `json:"clientToken"`
	Tags                        map[string]string `json:"tags"`
	AgentCollaboration          string            `json:"agentCollaboration"`
	OrchestrationType           string            `json:"orchestrationType"`
	CustomerEncryptionKeyArn    string            `json:"customerEncryptionKeyArn"`
	PromptOverrideConfiguration json.RawMessage   `json:"promptOverrideConfiguration"`
	GuardrailConfiguration      json.RawMessage   `json:"guardrailConfiguration"`
	MemoryConfiguration         json.RawMessage   `json:"memoryConfiguration"`
	CustomOrchestration         json.RawMessage   `json:"customOrchestration"`
}

// config maps a Create/UpdateAgent body onto the driver's AgentConfig.
func (in *createAgentRequest) config() badriver.AgentConfig {
	return badriver.AgentConfig{
		Name:                        in.AgentName,
		ResourceRoleArn:             in.AgentResourceRoleArn,
		FoundationModel:             in.FoundationModel,
		Instruction:                 in.Instruction,
		Description:                 in.Description,
		IdleSessionTTLInSeconds:     in.IdleSessionTTLInSeconds,
		ClientToken:                 in.ClientToken,
		Tags:                        in.Tags,
		AgentCollaboration:          in.AgentCollaboration,
		OrchestrationType:           in.OrchestrationType,
		CustomerEncryptionKeyArn:    in.CustomerEncryptionKeyArn,
		PromptOverrideConfiguration: in.PromptOverrideConfiguration,
		GuardrailConfiguration:      in.GuardrailConfiguration,
		MemoryConfiguration:         in.MemoryConfiguration,
		CustomOrchestration:         in.CustomOrchestration,
	}
}

type agentJSON struct {
	AgentID                  string `json:"agentId"`
	AgentARN                 string `json:"agentArn"`
	AgentName                string `json:"agentName"`
	AgentResourceRoleArn     string `json:"agentResourceRoleArn,omitempty"`
	FoundationModel          string `json:"foundationModel,omitempty"`
	Instruction              string `json:"instruction,omitempty"`
	Description              string `json:"description,omitempty"`
	AgentStatus              string `json:"agentStatus"`
	AgentVersion             string `json:"agentVersion"`
	IdleSessionTTLInSeconds  int32  `json:"idleSessionTTLInSeconds"`
	CreatedAt                string `json:"createdAt"`
	UpdatedAt                string `json:"updatedAt"`
	PreparedAt               string `json:"preparedAt,omitempty"`
	AgentCollaboration       string `json:"agentCollaboration,omitempty"`
	OrchestrationType        string `json:"orchestrationType,omitempty"`
	CustomerEncryptionKeyArn string `json:"customerEncryptionKeyArn,omitempty"`
	// PromptOverrideConfiguration is always present on a real agent: the
	// caller's override, or an empty list (AWS's default prompt templates are
	// not reproduced).
	PromptOverrideConfiguration json.RawMessage `json:"promptOverrideConfiguration"`
	GuardrailConfiguration      json.RawMessage `json:"guardrailConfiguration,omitempty"`
	MemoryConfiguration         json.RawMessage `json:"memoryConfiguration,omitempty"`
	CustomOrchestration         json.RawMessage `json:"customOrchestration,omitempty"`
}

type agentSummaryJSON struct {
	AgentID     string `json:"agentId"`
	AgentName   string `json:"agentName"`
	AgentStatus string `json:"agentStatus"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
}

type agentEnvelope struct {
	Agent agentJSON `json:"agent"`
}

type listAgentsResponse struct {
	AgentSummaries []agentSummaryJSON `json:"agentSummaries"`
	NextToken      string             `json:"nextToken,omitempty"`
}

type deleteAgentResponse struct {
	AgentID     string `json:"agentId"`
	AgentStatus string `json:"agentStatus"`
}

type prepareAgentResponse struct {
	AgentID      string `json:"agentId"`
	AgentStatus  string `json:"agentStatus"`
	AgentVersion string `json:"agentVersion"`
	PreparedAt   string `json:"preparedAt"`
}

// --- knowledge bases ---

type createKnowledgeBaseRequest struct {
	Name                       string            `json:"name"`
	RoleArn                    string            `json:"roleArn"`
	Description                string            `json:"description"`
	KnowledgeBaseConfiguration json.RawMessage   `json:"knowledgeBaseConfiguration"`
	StorageConfiguration       json.RawMessage   `json:"storageConfiguration"`
	Tags                       map[string]string `json:"tags"`
}

type knowledgeBaseJSON struct {
	KnowledgeBaseID            string          `json:"knowledgeBaseId"`
	KnowledgeBaseARN           string          `json:"knowledgeBaseArn"`
	Name                       string          `json:"name"`
	RoleArn                    string          `json:"roleArn"`
	Description                string          `json:"description,omitempty"`
	Status                     string          `json:"status"`
	KnowledgeBaseConfiguration json.RawMessage `json:"knowledgeBaseConfiguration,omitempty"`
	StorageConfiguration       json.RawMessage `json:"storageConfiguration,omitempty"`
	CreatedAt                  string          `json:"createdAt"`
	UpdatedAt                  string          `json:"updatedAt"`
}

type knowledgeBaseSummaryJSON struct {
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Description     string `json:"description,omitempty"`
	UpdatedAt       string `json:"updatedAt"`
}

type knowledgeBaseEnvelope struct {
	KnowledgeBase knowledgeBaseJSON `json:"knowledgeBase"`
}

type listKnowledgeBasesResponse struct {
	KnowledgeBaseSummaries []knowledgeBaseSummaryJSON `json:"knowledgeBaseSummaries"`
	NextToken              string                     `json:"nextToken,omitempty"`
}

type deleteKnowledgeBaseResponse struct {
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	Status          string `json:"status"`
}

// --- data sources ---

type createDataSourceRequest struct {
	Name                         string          `json:"name"`
	Description                  string          `json:"description"`
	DataDeletionPolicy           string          `json:"dataDeletionPolicy"`
	DataSourceConfiguration      json.RawMessage `json:"dataSourceConfiguration"`
	VectorIngestionConfiguration json.RawMessage `json:"vectorIngestionConfiguration"`
}

type dataSourceJSON struct {
	DataSourceID            string          `json:"dataSourceId"`
	KnowledgeBaseID         string          `json:"knowledgeBaseId"`
	Name                    string          `json:"name"`
	Description             string          `json:"description,omitempty"`
	Status                  string          `json:"status"`
	DataDeletionPolicy      string          `json:"dataDeletionPolicy,omitempty"`
	DataSourceConfiguration json.RawMessage `json:"dataSourceConfiguration,omitempty"`
	CreatedAt               string          `json:"createdAt"`
	UpdatedAt               string          `json:"updatedAt"`
}

type dataSourceSummaryJSON struct {
	DataSourceID    string `json:"dataSourceId"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Description     string `json:"description,omitempty"`
	UpdatedAt       string `json:"updatedAt"`
}

type dataSourceEnvelope struct {
	DataSource dataSourceJSON `json:"dataSource"`
}

type listDataSourcesResponse struct {
	DataSourceSummaries []dataSourceSummaryJSON `json:"dataSourceSummaries"`
	NextToken           string                  `json:"nextToken,omitempty"`
}

type deleteDataSourceResponse struct {
	DataSourceID    string `json:"dataSourceId"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	Status          string `json:"status"`
}

type startIngestionJobRequest struct {
	Description string `json:"description"`
}

type ingestionJobJSON struct {
	IngestionJobID  string                   `json:"ingestionJobId"`
	KnowledgeBaseID string                   `json:"knowledgeBaseId"`
	DataSourceID    string                   `json:"dataSourceId"`
	Description     string                   `json:"description,omitempty"`
	Status          string                   `json:"status"`
	Statistics      *ingestionStatisticsJSON `json:"statistics,omitempty"`
	FailureReasons  []string                 `json:"failureReasons,omitempty"`
	StartedAt       string                   `json:"startedAt"`
	UpdatedAt       string                   `json:"updatedAt"`
}

type ingestionJobEnvelope struct {
	IngestionJob ingestionJobJSON `json:"ingestionJob"`
}

// --- flows (flat responses) ---

type createFlowRequest struct {
	Name                     string            `json:"name"`
	ExecutionRoleArn         string            `json:"executionRoleArn"`
	Description              string            `json:"description"`
	CustomerEncryptionKeyArn string            `json:"customerEncryptionKeyArn"`
	Definition               json.RawMessage   `json:"definition"`
	Tags                     map[string]string `json:"tags"`
}

type flowJSON struct {
	Arn                      string          `json:"arn"`
	ID                       string          `json:"id"`
	Name                     string          `json:"name"`
	Status                   string          `json:"status"`
	Version                  string          `json:"version"`
	ExecutionRoleArn         string          `json:"executionRoleArn"`
	Description              string          `json:"description,omitempty"`
	CustomerEncryptionKeyArn string          `json:"customerEncryptionKeyArn,omitempty"`
	Definition               json.RawMessage `json:"definition,omitempty"`
	CreatedAt                string          `json:"createdAt"`
	UpdatedAt                string          `json:"updatedAt"`
}

type flowSummaryJSON struct {
	Arn         string `json:"arn"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type listFlowsResponse struct {
	FlowSummaries []flowSummaryJSON `json:"flowSummaries"`
	NextToken     string            `json:"nextToken,omitempty"`
}

type deleteFlowResponse struct {
	ID string `json:"id"`
}

type prepareFlowResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// --- prompts (flat responses) ---

type createPromptRequest struct {
	Name                     string            `json:"name"`
	Description              string            `json:"description"`
	DefaultVariant           string            `json:"defaultVariant"`
	CustomerEncryptionKeyArn string            `json:"customerEncryptionKeyArn"`
	Variants                 json.RawMessage   `json:"variants"`
	Tags                     map[string]string `json:"tags"`
}

type promptJSON struct {
	Arn                      string          `json:"arn"`
	ID                       string          `json:"id"`
	Name                     string          `json:"name"`
	Version                  string          `json:"version"`
	Description              string          `json:"description,omitempty"`
	DefaultVariant           string          `json:"defaultVariant,omitempty"`
	CustomerEncryptionKeyArn string          `json:"customerEncryptionKeyArn,omitempty"`
	Variants                 json.RawMessage `json:"variants,omitempty"`
	CreatedAt                string          `json:"createdAt"`
	UpdatedAt                string          `json:"updatedAt"`
}

type promptSummaryJSON struct {
	Arn         string `json:"arn"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type listPromptsResponse struct {
	PromptSummaries []promptSummaryJSON `json:"promptSummaries"`
	NextToken       string              `json:"nextToken,omitempty"`
}

type deletePromptResponse struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

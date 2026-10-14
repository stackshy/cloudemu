package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// ControlPlane is the part of the bedrock-agent control plane the runtime
// reads. The bedrock-agent mock satisfies it.
type ControlPlane interface {
	GetAgent(ctx context.Context, agentID string) (*badriver.Agent, error)
	GetAgentAlias(ctx context.Context, agentID, aliasID string) (*badriver.AgentAlias, error)
	GetAgentVersion(ctx context.Context, agentID, version string) (*badriver.Agent, error)
	ListAgentActionGroups(ctx context.Context, agentID, version string, page badriver.Page) ([]badriver.ActionGroup, string, error)
	ListAgentKnowledgeBases(ctx context.Context, agentID, version string, page badriver.Page) (
		[]badriver.AgentKnowledgeBase, string, error)
	GetKnowledgeBase(ctx context.Context, id string) (*badriver.KnowledgeBase, error)
	GetDataSource(ctx context.Context, kbID, dsID string) (*badriver.DataSource, error)
	GetFlow(ctx context.Context, id string) (*badriver.Flow, error)
	GetFlowVersion(ctx context.Context, flowID, version string) (*badriver.Flow, error)
	GetFlowAlias(ctx context.Context, flowID, aliasID string) (*badriver.FlowAlias, error)
	IndexedDocuments(kbID string) []badriver.IndexedDocument
}

// Models generates model answers (the Bedrock model runtime).
type Models interface {
	Converse(ctx context.Context, in bedrockdriver.ConverseInput) (*bedrockdriver.ConverseOutput, error)
}

// Guardrails evaluates content against a Bedrock guardrail.
type Guardrails interface {
	ApplyGuardrail(ctx context.Context, in bedrockdriver.ApplyGuardrailInput) (*bedrockdriver.ApplyGuardrailOutput, error)
}

// Lambda invokes Lambda functions (Lambda-backed action groups).
type Lambda interface {
	Invoke(ctx context.Context, in serverlessdriver.InvokeInput) (*serverlessdriver.InvokeOutput, error)
}

// maxListPage is the page size the runtime uses to read control-plane lists.
const maxListPage int32 = 1000

// actionGroup is an action group as the runtime runs it.
type actionGroup struct {
	name          string
	description   string
	returnControl bool
	lambdaARN     string
	functions     []functionDef
	apis          []apiDef
}

type functionDef struct {
	name        string
	description string
	params      []paramDef
}

type paramDef struct {
	name     string
	typ      string
	required bool
}

type apiDef struct {
	path   string
	method string
}

// knowledgeBaseRef is a knowledge base an agent searches.
type knowledgeBaseRef struct {
	id              string
	numberOfResults *int32
	filter          *retrievalFilter
}

// agentConfig is everything one agent turn needs, resolved from the control
// plane (or from an inline definition).
type agentConfig struct {
	name            string
	agentID         string
	aliasID         string
	aliasARN        string
	version         string
	foundationModel string
	instruction     string
	idleTTLSeconds  int32
	memoryEnabled   bool
	memoryDays      int32
	actionGroups    []actionGroup
	knowledgeBases  []knowledgeBaseRef
	guardrail       *guardrailRef
	operation       string
	preProcessing   bool // promptOverrideConfiguration enables the step
	postProcessing  bool
}

type guardrailRef struct {
	id      string
	version string
}

// functionSchemaJSON is the functionSchema union as the control plane stores it.
type functionSchemaJSON struct {
	Functions []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  map[string]struct {
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"parameters"`
	} `json:"functions"`
}

// executorJSON is the actionGroupExecutor union.
type executorJSON struct {
	Lambda        string `json:"lambda"`
	CustomControl string `json:"customControl"`
}

// apiSchemaJSON is the apiSchema union; only an inline JSON OpenAPI payload is
// read (an S3 schema or a YAML payload exposes no APIs to the emulator).
type apiSchemaJSON struct {
	Payload string `json:"payload"`
}

// toActionGroup converts a stored, enabled action group the runtime can call.
// The AMAZON.* and ANTHROPIC.* built-in signatures are not callable actions.
func toActionGroup(g *badriver.ActionGroup) (actionGroup, bool) {
	if g.State == badriver.ActionGroupDisabled || g.ParentActionGroupSignature != "" {
		return actionGroup{}, false
	}

	out := actionGroup{name: g.Name, description: g.Description}

	var ex executorJSON
	if json.Unmarshal(g.Executor, &ex) == nil {
		out.returnControl = ex.CustomControl == "RETURN_CONTROL"
		out.lambdaARN = ex.Lambda
	}

	var fs functionSchemaJSON
	if json.Unmarshal(g.FunctionSchema, &fs) == nil {
		for _, f := range fs.Functions {
			fd := functionDef{name: f.Name, description: f.Description}

			for _, name := range sortedKeys(f.Parameters) {
				p := f.Parameters[name]
				fd.params = append(fd.params, paramDef{name: name, typ: p.Type, required: p.Required})
			}

			out.functions = append(out.functions, fd)
		}
	}

	out.apis = parseOpenAPI(g.APISchema)

	return out, true
}

// parseOpenAPI lists the operations of an inline JSON OpenAPI document.
func parseOpenAPI(raw json.RawMessage) []apiDef {
	var schema apiSchemaJSON
	if json.Unmarshal(raw, &schema) != nil || schema.Payload == "" {
		return nil
	}

	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}

	if json.Unmarshal([]byte(schema.Payload), &doc) != nil {
		return nil
	}

	var out []apiDef

	for _, path := range sortedKeys(doc.Paths) {
		for _, method := range sortedKeys(doc.Paths[path]) {
			out = append(out, apiDef{path: path, method: strings.ToUpper(method)})
		}
	}

	return out
}

// agentConfigJSON pieces of the agent's stored configuration.
type guardrailConfigJSON struct {
	GuardrailIdentifier string `json:"guardrailIdentifier"`
	GuardrailVersion    string `json:"guardrailVersion"`
}

type memoryConfigJSON struct {
	EnabledMemoryTypes []string `json:"enabledMemoryTypes"`
	StorageDays        int32    `json:"storageDays"`
}

type promptOverrideJSON struct {
	PromptConfigurations []struct {
		PromptType  string `json:"promptType"`
		PromptState string `json:"promptState"`
	} `json:"promptConfigurations"`
}

// ObjectReader is the part of S3 the runtime reads external sources from.
type ObjectReader interface {
	GetObject(ctx context.Context, bucket, key string) (*storagedriver.Object, error)
}

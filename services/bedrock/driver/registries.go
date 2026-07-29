package driver

// Inference-profile status/type values.
const (
	InferenceProfileStatusActive      = "ACTIVE"
	InferenceProfileTypeApplication   = "APPLICATION"
	InferenceProfileTypeSystemDefined = "SYSTEM_DEFINED"
)

// Prompt-router status/type values.
const (
	PromptRouterStatusAvailable = "AVAILABLE"
	PromptRouterTypeCustom      = "custom"
	PromptRouterTypeDefault     = "default"
)

// AutomatedReasoningPolicyVersionDraft is the version assigned to newly created
// automated reasoning policies.
const AutomatedReasoningPolicyVersionDraft = "DRAFT"

// InferenceProfileConfig describes an application inference profile to create.
// ModelSourceCopyFrom is the source model ARN from the modelSource copyFrom
// union member.
type InferenceProfileConfig struct {
	Name                string
	ModelSourceCopyFrom string
	ClientRequestToken  string
	Description         string
	Tags                map[string]string
}

// InferenceProfile describes an application inference profile. Models holds the
// tracked model ARNs.
type InferenceProfile struct {
	ARN         string
	ID          string
	Name        string
	Models      []string
	Status      string
	Type        string
	Description string
	CreatedAt   string
	UpdatedAt   string
}

// PromptRouterConfig describes a prompt router to create. Models and
// FallbackModelARN carry model ARNs; ResponseQualityDifference is the routing
// criterion.
type PromptRouterConfig struct {
	Name                      string
	Models                    []string
	ResponseQualityDifference *float64
	FallbackModelARN          string
	ClientRequestToken        string
	Description               string
	Tags                      map[string]string
}

// PromptRouter describes a prompt router.
type PromptRouter struct {
	ARN                       string
	Name                      string
	Models                    []string
	ResponseQualityDifference *float64
	FallbackModelARN          string
	Status                    string
	Type                      string
	Description               string
	CreatedAt                 string
	UpdatedAt                 string
}

// AutomatedReasoningPolicyConfig describes an automated reasoning policy to
// create. PolicyDefinition is an opaque JSON document carried through verbatim.
type AutomatedReasoningPolicyConfig struct {
	Name               string
	ClientRequestToken string
	Description        string
	KMSKeyID           string
	PolicyDefinition   []byte
	Tags               map[string]string
}

// AutomatedReasoningPolicyUpdate describes an update to an existing automated
// reasoning policy.
type AutomatedReasoningPolicyUpdate struct {
	PolicyDefinition []byte
	Description      string
	Name             string
}

// AutomatedReasoningPolicy describes an automated reasoning policy.
type AutomatedReasoningPolicy struct {
	ARN              string
	ID               string
	Name             string
	Version          string
	DefinitionHash   string
	Description      string
	KMSKeyARN        string
	PolicyDefinition []byte
	CreatedAt        string
	UpdatedAt        string
}

package driver

// Guardrail topic type values.
const (
	GuardrailTopicDeny = "DENY"
)

// Guardrail content-filter strength values.
const (
	GuardrailStrengthNone   = "NONE"
	GuardrailStrengthLow    = "LOW"
	GuardrailStrengthMedium = "MEDIUM"
	GuardrailStrengthHigh   = "HIGH"
)

// Guardrail PII / regex action values.
const (
	GuardrailPiiActionBlock     = "BLOCK"
	GuardrailPiiActionAnonymize = "ANONYMIZE"
	GuardrailPiiActionNone      = "NONE"
)

// Guardrail contextual-grounding action values.
const (
	GuardrailGroundingActionBlock = "BLOCK"
	GuardrailGroundingActionNone  = "NONE"
)

// GuardrailTopic is a single denied topic in a topic policy.
type GuardrailTopic struct {
	Name       string
	Definition string
	Examples   []string
	Type       string
}

// GuardrailTopicPolicy denies conversation on named topics.
type GuardrailTopicPolicy struct {
	Topics []GuardrailTopic
}

// GuardrailContentFilter is a single harmful-content filter.
type GuardrailContentFilter struct {
	Type           string
	InputStrength  string
	OutputStrength string
}

// GuardrailContentPolicy configures harmful-content filters.
type GuardrailContentPolicy struct {
	Filters []GuardrailContentFilter
}

// GuardrailWord is a single denied word or phrase.
type GuardrailWord struct {
	Text string
}

// GuardrailManagedWordList selects a managed word list (e.g. PROFANITY).
type GuardrailManagedWordList struct {
	Type string
}

// GuardrailWordPolicy configures denied words and managed word lists.
type GuardrailWordPolicy struct {
	Words            []GuardrailWord
	ManagedWordLists []GuardrailManagedWordList
}

// GuardrailPiiEntity configures handling of a PII entity type.
type GuardrailPiiEntity struct {
	Type   string
	Action string
}

// GuardrailRegex configures handling of a custom regular expression.
type GuardrailRegex struct {
	Name        string
	Pattern     string
	Action      string
	Description string
}

// GuardrailSensitiveInformationPolicy configures PII and regex handling.
type GuardrailSensitiveInformationPolicy struct {
	PiiEntities []GuardrailPiiEntity
	Regexes     []GuardrailRegex
}

// GuardrailContextualGroundingFilter is a single grounding/relevance filter.
type GuardrailContextualGroundingFilter struct {
	Type      string
	Threshold float64
	Action    string
}

// GuardrailContextualGroundingPolicy configures grounding and relevance checks.
type GuardrailContextualGroundingPolicy struct {
	Filters []GuardrailContextualGroundingFilter
}

// GuardrailPolicies bundles the five configurable guardrail policies. It is
// embedded in both GuardrailConfig (request) and Guardrail (stored/response).
type GuardrailPolicies struct {
	TopicPolicy                *GuardrailTopicPolicy
	ContentPolicy              *GuardrailContentPolicy
	WordPolicy                 *GuardrailWordPolicy
	SensitiveInformationPolicy *GuardrailSensitiveInformationPolicy
	ContextualGroundingPolicy  *GuardrailContextualGroundingPolicy
}

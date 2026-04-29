package monitor

// ARM JSON shapes for microsoft.insights metric alerts.

type alertRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties alertRequestProps `json:"properties"`
}

type alertRequestProps struct {
	Description    string           `json:"description,omitempty"`
	Severity       int              `json:"severity,omitempty"`
	Enabled        bool             `json:"enabled,omitempty"`
	Scopes         []string         `json:"scopes,omitempty"`
	EvaluationFreq string           `json:"evaluationFrequency,omitempty"`
	WindowSize     string           `json:"windowSize,omitempty"`
	Criteria       map[string]any   `json:"criteria,omitempty"`
	AutoMitigate   bool             `json:"autoMitigate,omitempty"`
	Actions        []map[string]any `json:"actions,omitempty"`
}

type alertResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Properties alertResponseProps `json:"properties"`
}

type alertResponseProps struct {
	alertRequestProps
	ProvisioningState string `json:"provisioningState"`
}

type alertListResponse struct {
	Value []alertResponse `json:"value"`
}

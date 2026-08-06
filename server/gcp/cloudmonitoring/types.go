package cloudmonitoring

// GCP Cloud Monitoring REST shapes.

type alertPolicy struct {
	Name                 string            `json:"name,omitempty"`
	DisplayName          string            `json:"displayName,omitempty"`
	Documentation        any               `json:"documentation,omitempty"`
	UserLabels           map[string]string `json:"userLabels,omitempty"`
	Conditions           []alertCondition  `json:"conditions,omitempty"`
	Combiner             string            `json:"combiner,omitempty"`
	Enabled              bool              `json:"enabled,omitempty"`
	NotificationChannels []string          `json:"notificationChannels,omitempty"`
	CreationRecord       any               `json:"creationRecord,omitempty"`
	MutationRecord       any               `json:"mutationRecord,omitempty"`
}

// alertCondition round-trips every Cloud Monitoring condition variant, not just
// conditionThreshold — conditionAbsent / MQL / PromQL / matchedLog are carried
// verbatim so they survive a create→read cycle instead of being silently dropped.
type alertCondition struct {
	Name                             string `json:"name,omitempty"`
	DisplayName                      string `json:"displayName,omitempty"`
	ConditionThreshold               any    `json:"conditionThreshold,omitempty"`
	ConditionAbsent                  any    `json:"conditionAbsent,omitempty"`
	ConditionMatchedLog              any    `json:"conditionMatchedLog,omitempty"`
	ConditionMonitoringQueryLanguage any    `json:"conditionMonitoringQueryLanguage,omitempty"`
	ConditionPrometheusQueryLanguage any    `json:"conditionPrometheusQueryLanguage,omitempty"`
}

type alertPoliciesList struct {
	AlertPolicies []alertPolicy `json:"alertPolicies"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
}

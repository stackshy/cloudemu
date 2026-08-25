package monitoring

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

// mutationRecord mirrors the Cloud Monitoring MutationRecord shape populated on
// alertPolicies.creationRecord / mutationRecord.
type mutationRecord struct {
	MutateTime string `json:"mutateTime,omitempty"`
	MutatedBy  string `json:"mutatedBy,omitempty"`
}

// timeSeries is one Cloud Monitoring time series: a metric+resource label set
// and its points.
type timeSeries struct {
	Metric     metricRef    `json:"metric"`
	Resource   monitoredRes `json:"resource"`
	MetricKind string       `json:"metricKind,omitempty"`
	ValueType  string       `json:"valueType,omitempty"`
	Points     []point      `json:"points"`
}

type metricRef struct {
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
}

type monitoredRes struct {
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
}

type point struct {
	Interval timeInterval `json:"interval"`
	Value    typedValue   `json:"value"`
}

type timeInterval struct {
	StartTime string `json:"startTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`
}

type typedValue struct {
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	Int64Value  *string  `json:"int64Value,omitempty"`
}

type listTimeSeriesResponse struct {
	TimeSeries    []timeSeries `json:"timeSeries"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

type createTimeSeriesRequest struct {
	TimeSeries []timeSeries `json:"timeSeries"`
}

// metricDescriptor is the Cloud Monitoring MetricDescriptor shape.
type metricDescriptor struct {
	Name        string            `json:"name,omitempty"`
	Type        string            `json:"type"`
	MetricKind  string            `json:"metricKind,omitempty"`
	ValueType   string            `json:"valueType,omitempty"`
	Unit        string            `json:"unit,omitempty"`
	DisplayName string            `json:"displayName,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      []descriptorLabel `json:"labels,omitempty"`
}

type descriptorLabel struct {
	Key         string `json:"key"`
	ValueType   string `json:"valueType,omitempty"`
	Description string `json:"description,omitempty"`
}

type metricDescriptorsList struct {
	MetricDescriptors []metricDescriptor `json:"metricDescriptors"`
	NextPageToken     string             `json:"nextPageToken,omitempty"`
}

// notificationChannel is the Cloud Monitoring NotificationChannel shape.
type notificationChannel struct {
	Name               string            `json:"name,omitempty"`
	Type               string            `json:"type,omitempty"`
	DisplayName        string            `json:"displayName,omitempty"`
	Description        string            `json:"description,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	UserLabels         map[string]string `json:"userLabels,omitempty"`
	VerificationStatus string            `json:"verificationStatus,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
}

type notificationChannelsList struct {
	NotificationChannels []notificationChannel `json:"notificationChannels"`
	NextPageToken        string                `json:"nextPageToken,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
}

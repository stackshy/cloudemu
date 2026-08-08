package monitoring

import "time"

// OCI Monitoring REST shapes, as documented under /20180401.

type postMetricDataRequest struct {
	MetricData     []metricDataDetails `json:"metricData"`
	BatchAtomicity string              `json:"batchAtomicity,omitempty"`
}

type metricDataDetails struct {
	Namespace     string            `json:"namespace"`
	ResourceGroup string            `json:"resourceGroup,omitempty"`
	CompartmentID string            `json:"compartmentId"`
	Name          string            `json:"name"`
	Dimensions    map[string]string `json:"dimensions,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Datapoints    []datapoint       `json:"datapoints"`
}

type datapoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Count     *int      `json:"count,omitempty"`
}

type postMetricDataResponse struct {
	FailedMetricsCount int                 `json:"failedMetricsCount"`
	FailedMetrics      []metricDataDetails `json:"failedMetrics"`
}

type listMetricsDetails struct {
	Name             string            `json:"name,omitempty"`
	Namespace        string            `json:"namespace,omitempty"`
	ResourceGroup    string            `json:"resourceGroup,omitempty"`
	DimensionFilters map[string]string `json:"dimensionFilters,omitempty"`
	GroupBy          []string          `json:"groupBy,omitempty"`
	SortBy           string            `json:"sortBy,omitempty"`
	SortOrder        string            `json:"sortOrder,omitempty"`
}

type metric struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	ResourceGroup string            `json:"resourceGroup,omitempty"`
	CompartmentID string            `json:"compartmentId"`
	Dimensions    map[string]string `json:"dimensions,omitempty"`
}

type summarizeMetricsDataDetails struct {
	Namespace     string     `json:"namespace"`
	ResourceGroup string     `json:"resourceGroup,omitempty"`
	Query         string     `json:"query"`
	StartTime     *time.Time `json:"startTime,omitempty"`
	EndTime       *time.Time `json:"endTime,omitempty"`
	Resolution    string     `json:"resolution,omitempty"`
}

type metricData struct {
	Namespace            string                `json:"namespace"`
	ResourceGroup        string                `json:"resourceGroup,omitempty"`
	CompartmentID        string                `json:"compartmentId"`
	Name                 string                `json:"name"`
	Dimensions           map[string]string     `json:"dimensions,omitempty"`
	Resolution           string                `json:"resolution,omitempty"`
	AggregatedDatapoints []aggregatedDatapoint `json:"aggregatedDatapoints"`
}

type aggregatedDatapoint struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

// alarmDetails is the request body of CreateAlarm and UpdateAlarm. Pointer
// fields distinguish an omitted field from an explicit zero on update.
type alarmDetails struct {
	DisplayName                string                    `json:"displayName,omitempty"`
	CompartmentID              string                    `json:"compartmentId,omitempty"`
	MetricCompartmentID        string                    `json:"metricCompartmentId,omitempty"`
	Namespace                  string                    `json:"namespace,omitempty"`
	ResourceGroup              string                    `json:"resourceGroup,omitempty"`
	Query                      string                    `json:"query,omitempty"`
	Resolution                 string                    `json:"resolution,omitempty"`
	PendingDuration            string                    `json:"pendingDuration,omitempty"`
	Severity                   string                    `json:"severity,omitempty"`
	Body                       string                    `json:"body,omitempty"`
	MessageFormat              string                    `json:"messageFormat,omitempty"`
	RepeatNotificationDuration string                    `json:"repeatNotificationDuration,omitempty"`
	Destinations               []string                  `json:"destinations,omitempty"`
	FreeformTags               map[string]string         `json:"freeformTags,omitempty"`
	DefinedTags                map[string]map[string]any `json:"definedTags,omitempty"`
	IsEnabled                  *bool                     `json:"isEnabled,omitempty"`
	Suppression                map[string]any            `json:"suppression,omitempty"`
	Overrides                  []map[string]any          `json:"overrides,omitempty"`
}

type alarm struct {
	ID                           string                    `json:"id"`
	DisplayName                  string                    `json:"displayName"`
	CompartmentID                string                    `json:"compartmentId"`
	MetricCompartmentID          string                    `json:"metricCompartmentId"`
	MetricCompartmentIDInSubtree bool                      `json:"metricCompartmentIdInSubtree"`
	Namespace                    string                    `json:"namespace"`
	ResourceGroup                string                    `json:"resourceGroup,omitempty"`
	Query                        string                    `json:"query"`
	Resolution                   string                    `json:"resolution,omitempty"`
	PendingDuration              string                    `json:"pendingDuration,omitempty"`
	Severity                     string                    `json:"severity"`
	Body                         string                    `json:"body,omitempty"`
	MessageFormat                string                    `json:"messageFormat,omitempty"`
	RepeatNotificationDuration   string                    `json:"repeatNotificationDuration,omitempty"`
	Destinations                 []string                  `json:"destinations"`
	FreeformTags                 map[string]string         `json:"freeformTags,omitempty"`
	DefinedTags                  map[string]map[string]any `json:"definedTags,omitempty"`
	IsEnabled                    bool                      `json:"isEnabled"`
	LifecycleState               string                    `json:"lifecycleState"`
	TimeCreated                  string                    `json:"timeCreated"`
	TimeUpdated                  string                    `json:"timeUpdated"`
}

type alarmStatusSummary struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"displayName"`
	Severity           string `json:"severity"`
	Status             string `json:"status"`
	TimestampTriggered string `json:"timestampTriggered,omitempty"`
}

type alarmHistoryCollection struct {
	AlarmID   string              `json:"alarmId"`
	IsEnabled bool                `json:"isEnabled"`
	Entries   []alarmHistoryEntry `json:"entries"`
}

type alarmHistoryEntry struct {
	Timestamp          string `json:"timestamp"`
	TimestampTriggered string `json:"timestampTriggered,omitempty"`
	Summary            string `json:"summary"`
}

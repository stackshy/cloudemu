package logging

// OCI Logging REST shapes, across all three API surfaces.

// definedTags is OCI's namespaced tag map. CloudEmu does not model tag
// namespaces, so it is echoed back empty.
type definedTags map[string]map[string]any

// Control plane — /20200531.

type createLogGroupRequest struct {
	CompartmentID string            `json:"compartmentId"`
	DisplayName   string            `json:"displayName"`
	Description   string            `json:"description,omitempty"`
	FreeformTags  map[string]string `json:"freeformTags,omitempty"`
	DefinedTags   definedTags       `json:"definedTags,omitempty"`
}

type updateLogGroupRequest struct {
	DisplayName  *string           `json:"displayName,omitempty"`
	Description  *string           `json:"description,omitempty"`
	FreeformTags map[string]string `json:"freeformTags,omitempty"`
	DefinedTags  definedTags       `json:"definedTags,omitempty"`
}

type changeCompartmentRequest struct {
	TargetCompartmentID string `json:"targetCompartmentId"`
}

type logGroupResponse struct {
	ID               string            `json:"id"`
	CompartmentID    string            `json:"compartmentId"`
	DisplayName      string            `json:"displayName"`
	Description      string            `json:"description,omitempty"`
	LifecycleState   string            `json:"lifecycleState"`
	TimeCreated      string            `json:"timeCreated"`
	TimeLastModified string            `json:"timeLastModified"`
	FreeformTags     map[string]string `json:"freeformTags"`
	DefinedTags      definedTags       `json:"definedTags"`
}

// logSourceBody is the source clause of a log's configuration.
type logSourceBody struct {
	SourceType string            `json:"sourceType,omitempty"`
	Service    string            `json:"service,omitempty"`
	Resource   string            `json:"resource,omitempty"`
	Category   string            `json:"category,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

type archivingBody struct {
	IsEnabled bool `json:"isEnabled"`
}

type logConfigurationBody struct {
	CompartmentID string         `json:"compartmentId,omitempty"`
	Source        logSourceBody  `json:"source"`
	Archiving     *archivingBody `json:"archiving,omitempty"`
}

type createLogRequest struct {
	DisplayName       string                `json:"displayName"`
	LogType           string                `json:"logType"`
	IsEnabled         *bool                 `json:"isEnabled,omitempty"`
	RetentionDuration *int                  `json:"retentionDuration,omitempty"`
	Configuration     *logConfigurationBody `json:"configuration,omitempty"`
	FreeformTags      map[string]string     `json:"freeformTags,omitempty"`
	DefinedTags       definedTags           `json:"definedTags,omitempty"`
}

type updateLogRequest struct {
	DisplayName       *string               `json:"displayName,omitempty"`
	IsEnabled         *bool                 `json:"isEnabled,omitempty"`
	RetentionDuration *int                  `json:"retentionDuration,omitempty"`
	Configuration     *logConfigurationBody `json:"configuration,omitempty"`
	FreeformTags      map[string]string     `json:"freeformTags,omitempty"`
	DefinedTags       definedTags           `json:"definedTags,omitempty"`
}

type logResponse struct {
	ID                string                `json:"id"`
	LogGroupID        string                `json:"logGroupId"`
	CompartmentID     string                `json:"compartmentId"`
	DisplayName       string                `json:"displayName"`
	LogType           string                `json:"logType"`
	IsEnabled         bool                  `json:"isEnabled"`
	LifecycleState    string                `json:"lifecycleState"`
	RetentionDuration int                   `json:"retentionDuration"`
	Configuration     *logConfigurationBody `json:"configuration,omitempty"`
	TimeCreated       string                `json:"timeCreated"`
	TimeLastModified  string                `json:"timeLastModified"`
	FreeformTags      map[string]string     `json:"freeformTags"`
	DefinedTags       definedTags           `json:"definedTags"`
}

// Ingestion plane — /20200601.

type putLogsEntry struct {
	Data string `json:"data"`
	ID   string `json:"id,omitempty"`
	Time string `json:"time,omitempty"`
}

type putLogsBatch struct {
	Entries []putLogsEntry `json:"entries"`
	Source  string         `json:"source,omitempty"`
	Type    string         `json:"type,omitempty"`
	Subject string         `json:"subject,omitempty"`
	// DefaultLogEntryTime is spelled all-lowercase by the ingestion API.
	DefaultLogEntryTime string `json:"defaultlogentrytime,omitempty"`
}

type putLogsRequest struct {
	SpecVersion     string         `json:"specversion"`
	LogEntryBatches []putLogsBatch `json:"logEntryBatches"`
}

// Search plane — /20190909.

type searchLogsRequest struct {
	SearchQuery       string `json:"searchQuery"`
	TimeStart         string `json:"timeStart"`
	TimeEnd           string `json:"timeEnd"`
	IsReturnFieldInfo bool   `json:"isReturnFieldInfo,omitempty"`
}

// oracleFields are the log-provenance fields OCI stamps onto every record.
type oracleFields struct {
	CompartmentID string `json:"compartmentid"`
	IngestedTime  string `json:"ingestedtime"`
	LogGroupID    string `json:"loggroupid"`
	LogID         string `json:"logid"`
}

// logContent is the CloudEvents-shaped record a search returns. Data is the
// decoded payload when it is a JSON object, and the raw string otherwise.
type logContent struct {
	Data        any          `json:"data"`
	ID          string       `json:"id"`
	Oracle      oracleFields `json:"oracle"`
	Source      string       `json:"source"`
	SpecVersion string       `json:"specversion"`
	Subject     string       `json:"subject,omitempty"`
	Time        string       `json:"time"`
	Type        string       `json:"type,omitempty"`
}

type searchResultData struct {
	// Datetime is milliseconds since the epoch, which is how the search API
	// reports a record's time alongside the RFC 3339 one in logContent.
	Datetime   int64      `json:"datetime"`
	LogContent logContent `json:"logContent"`
}

type searchResult struct {
	Data searchResultData `json:"data"`
}

type fieldInfo struct {
	FieldName string `json:"fieldName"`
	FieldType string `json:"fieldType"`
}

type searchSummary struct {
	ResultCount int `json:"resultCount"`
	FieldCount  int `json:"fieldCount"`
}

type searchLogsResponse struct {
	Results []searchResult `json:"results"`
	Fields  []fieldInfo    `json:"fields,omitempty"`
	Summary searchSummary  `json:"summary"`
}

package athena

import "time"

// epochOrNil renders a time as a Unix-epoch float the Athena SDK decodes into a
// *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// --- nested wire shapes ---

type engineVersionJSON struct {
	SelectedEngineVersion  string `json:"SelectedEngineVersion,omitempty"`
	EffectiveEngineVersion string `json:"EffectiveEngineVersion,omitempty"`
}

type encryptionConfigJSON struct {
	EncryptionOption string `json:"EncryptionOption,omitempty"`
	KmsKey           string `json:"KmsKey,omitempty"`
}

type aclConfigJSON struct {
	S3AclOption string `json:"S3AclOption,omitempty"`
}

type resultConfigJSON struct {
	OutputLocation          string                `json:"OutputLocation,omitempty"`
	EncryptionConfiguration *encryptionConfigJSON `json:"EncryptionConfiguration,omitempty"`
	ExpectedBucketOwner     string                `json:"ExpectedBucketOwner,omitempty"`
	ACLConfiguration        *aclConfigJSON        `json:"AclConfiguration,omitempty"`
}

type resultConfigUpdatesJSON struct {
	OutputLocation                string                `json:"OutputLocation,omitempty"`
	RemoveOutputLocation          bool                  `json:"RemoveOutputLocation,omitempty"`
	EncryptionConfiguration       *encryptionConfigJSON `json:"EncryptionConfiguration,omitempty"`
	RemoveEncryptionConfiguration bool                  `json:"RemoveEncryptionConfiguration,omitempty"`
	ExpectedBucketOwner           string                `json:"ExpectedBucketOwner,omitempty"`
	RemoveExpectedBucketOwner     bool                  `json:"RemoveExpectedBucketOwner,omitempty"`
	ACLConfiguration              *aclConfigJSON        `json:"AclConfiguration,omitempty"`
	RemoveACLConfiguration        bool                  `json:"RemoveAclConfiguration,omitempty"`
}

type workGroupConfigJSON struct {
	ResultConfiguration             *resultConfigJSON  `json:"ResultConfiguration,omitempty"`
	EnforceWorkGroupConfiguration   *bool              `json:"EnforceWorkGroupConfiguration,omitempty"`
	PublishCloudWatchMetricsEnabled *bool              `json:"PublishCloudWatchMetricsEnabled,omitempty"`
	RequesterPaysEnabled            *bool              `json:"RequesterPaysEnabled,omitempty"`
	BytesScannedCutoffPerQuery      *int64             `json:"BytesScannedCutoffPerQuery,omitempty"`
	EngineVersion                   *engineVersionJSON `json:"EngineVersion,omitempty"`
}

type workGroupConfigUpdatesJSON struct {
	ResultConfigurationUpdates       *resultConfigUpdatesJSON `json:"ResultConfigurationUpdates,omitempty"`
	EnforceWorkGroupConfiguration    *bool                    `json:"EnforceWorkGroupConfiguration,omitempty"`
	PublishCloudWatchMetricsEnabled  *bool                    `json:"PublishCloudWatchMetricsEnabled,omitempty"`
	RequesterPaysEnabled             *bool                    `json:"RequesterPaysEnabled,omitempty"`
	BytesScannedCutoffPerQuery       *int64                   `json:"BytesScannedCutoffPerQuery,omitempty"`
	RemoveBytesScannedCutoffPerQuery bool                     `json:"RemoveBytesScannedCutoffPerQuery,omitempty"`
	EngineVersion                    *engineVersionJSON       `json:"EngineVersion,omitempty"`
}

type workGroupJSON struct {
	Name          string               `json:"Name"`
	State         string               `json:"State,omitempty"`
	Description   string               `json:"Description,omitempty"`
	Configuration *workGroupConfigJSON `json:"Configuration,omitempty"`
	CreationTime  *float64             `json:"CreationTime,omitempty"`
}

type workGroupSummaryJSON struct {
	Name          string             `json:"Name,omitempty"`
	State         string             `json:"State,omitempty"`
	Description   string             `json:"Description,omitempty"`
	CreationTime  *float64           `json:"CreationTime,omitempty"`
	EngineVersion *engineVersionJSON `json:"EngineVersion,omitempty"`
}

type namedQueryJSON struct {
	Name         string `json:"Name"`
	Description  string `json:"Description,omitempty"`
	Database     string `json:"Database"`
	QueryString  string `json:"QueryString"`
	NamedQueryID string `json:"NamedQueryId,omitempty"`
	WorkGroup    string `json:"WorkGroup,omitempty"`
}

type queryExecutionContextJSON struct {
	Database string `json:"Database,omitempty"`
	Catalog  string `json:"Catalog,omitempty"`
}

type queryExecutionStatusJSON struct {
	State              string   `json:"State,omitempty"`
	StateChangeReason  string   `json:"StateChangeReason,omitempty"`
	SubmissionDateTime *float64 `json:"SubmissionDateTime,omitempty"`
	CompletionDateTime *float64 `json:"CompletionDateTime,omitempty"`
}

type queryExecutionStatisticsJSON struct {
	EngineExecutionTimeInMillis int64 `json:"EngineExecutionTimeInMillis"`
	DataScannedInBytes          int64 `json:"DataScannedInBytes"`
	TotalExecutionTimeInMillis  int64 `json:"TotalExecutionTimeInMillis"`
}

type queryExecutionJSON struct {
	QueryExecutionID      string                        `json:"QueryExecutionId,omitempty"`
	Query                 string                        `json:"Query,omitempty"`
	StatementType         string                        `json:"StatementType,omitempty"`
	ResultConfiguration   *resultConfigJSON             `json:"ResultConfiguration,omitempty"`
	QueryExecutionContext *queryExecutionContextJSON    `json:"QueryExecutionContext,omitempty"`
	Status                *queryExecutionStatusJSON     `json:"Status,omitempty"`
	Statistics            *queryExecutionStatisticsJSON `json:"Statistics,omitempty"`
	WorkGroup             string                        `json:"WorkGroup,omitempty"`
	EngineVersion         *engineVersionJSON            `json:"EngineVersion,omitempty"`
}

type databaseJSON struct {
	Name        string            `json:"Name"`
	Description string            `json:"Description,omitempty"`
	Parameters  map[string]string `json:"Parameters,omitempty"`
}

type dataCatalogJSON struct {
	Name        string            `json:"Name"`
	Description string            `json:"Description,omitempty"`
	Type        string            `json:"Type,omitempty"`
	Parameters  map[string]string `json:"Parameters,omitempty"`
}

type dataCatalogSummaryJSON struct {
	CatalogName string `json:"CatalogName,omitempty"`
	Type        string `json:"Type,omitempty"`
}

package ssm

import (
	"time"

	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// parameterJSON is the wire shape for a Parameter, matching the fields the AWS
// SDK deserializes. LastModifiedDate is epoch seconds (AWS JSON timestamp form).
type parameterJSON struct {
	ARN              string  `json:"ARN,omitempty"`
	DataType         string  `json:"DataType,omitempty"`
	LastModifiedDate float64 `json:"LastModifiedDate,omitempty"`
	Name             string  `json:"Name"`
	Selector         string  `json:"Selector,omitempty"`
	Type             string  `json:"Type,omitempty"`
	Value            string  `json:"Value,omitempty"`
	Version          int64   `json:"Version"`
}

// parameterMetadataJSON is the wire shape for ParameterMetadata (DescribeParameters).
type parameterMetadataJSON struct {
	ARN              string  `json:"ARN,omitempty"`
	DataType         string  `json:"DataType,omitempty"`
	Description      string  `json:"Description,omitempty"`
	LastModifiedDate float64 `json:"LastModifiedDate,omitempty"`
	LastModifiedUser string  `json:"LastModifiedUser,omitempty"`
	Name             string  `json:"Name"`
	Tier             string  `json:"Tier,omitempty"`
	Type             string  `json:"Type,omitempty"`
	Version          int64   `json:"Version"`
}

// --- request envelopes ---

type putParameterRequest struct {
	Name        string `json:"Name"`
	Value       string `json:"Value"`
	Type        string `json:"Type"`
	Description string `json:"Description"`
	Overwrite   bool   `json:"Overwrite"`
	Tier        string `json:"Tier"`
	DataType    string `json:"DataType"`
}

type getParameterRequest struct {
	Name           string `json:"Name"`
	WithDecryption bool   `json:"WithDecryption"`
}

type getParametersRequest struct {
	Names          []string `json:"Names"`
	WithDecryption bool     `json:"WithDecryption"`
}

type getParametersByPathRequest struct {
	Path           string `json:"Path"`
	Recursive      bool   `json:"Recursive"`
	WithDecryption bool   `json:"WithDecryption"`
}

type nameRequest struct {
	Name string `json:"Name"`
}

type namesRequest struct {
	Names []string `json:"Names"`
}

type labelParameterVersionRequest struct {
	Name             string   `json:"Name"`
	ParameterVersion int64    `json:"ParameterVersion"`
	Labels           []string `json:"Labels"`
}

// --- response envelopes ---

type putParameterResponse struct {
	Tier    string `json:"Tier,omitempty"`
	Version int64  `json:"Version"`
}

type getParameterResponse struct {
	Parameter parameterJSON `json:"Parameter"`
}

type getParametersResponse struct {
	Parameters        []parameterJSON `json:"Parameters"`
	InvalidParameters []string        `json:"InvalidParameters,omitempty"`
}

type getParametersByPathResponse struct {
	Parameters []parameterJSON `json:"Parameters"`
}

type deleteParametersResponse struct {
	DeletedParameters []string `json:"DeletedParameters,omitempty"`
	InvalidParameters []string `json:"InvalidParameters,omitempty"`
}

type describeParametersResponse struct {
	Parameters []parameterMetadataJSON `json:"Parameters"`
}

type labelParameterVersionResponse struct {
	InvalidLabels    []string `json:"InvalidLabels,omitempty"`
	ParameterVersion int64    `json:"ParameterVersion"`
}

type getParameterHistoryResponse struct {
	Parameters []parameterHistoryJSON `json:"Parameters"`
}

// parameterHistoryJSON is the wire shape for a ParameterHistory entry.
type parameterHistoryJSON struct {
	ARN              string  `json:"ARN,omitempty"`
	DataType         string  `json:"DataType,omitempty"`
	LastModifiedDate float64 `json:"LastModifiedDate,omitempty"`
	Name             string  `json:"Name"`
	Type             string  `json:"Type,omitempty"`
	Value            string  `json:"Value,omitempty"`
	Version          int64   `json:"Version"`
}

// epochSeconds converts an RFC3339 timestamp to Unix epoch seconds, the form
// the AWS JSON protocol uses for timestamp fields. Returns 0 on parse failure.
func epochSeconds(iso string) float64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

func toParameterJSON(p ssmdriver.Parameter) parameterJSON {
	return parameterJSON{
		ARN:              p.ARN,
		DataType:         p.DataType,
		LastModifiedDate: epochSeconds(p.LastModified),
		Name:             p.Name,
		Selector:         p.Selector,
		Type:             p.Type,
		Value:            p.Value,
		Version:          p.Version,
	}
}

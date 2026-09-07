package kendra

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// tagJSON is the wire shape of a Kendra tag: an object with PascalCase
// Key/Value members.
type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToWire(tags []driver.Tag) []tagJSON {
	if tags == nil {
		return nil
	}

	out := make([]tagJSON, len(tags))
	for i, t := range tags {
		out[i] = tagJSON{Key: t.Key, Value: t.Value}
	}

	return out
}

func tagsFromWire(tags []tagJSON) []driver.Tag {
	if tags == nil {
		return nil
	}

	out := make([]driver.Tag, len(tags))
	for i, t := range tags {
		out[i] = driver.Tag{Key: t.Key, Value: t.Value}
	}

	return out
}

// epochSeconds renders a driver timestamp as the epoch-seconds number Kendra's
// wire uses for CreatedAt/UpdatedAt.
func epochSeconds(t time.Time) int64 {
	return t.Unix()
}

// indexDescriptionJSON is the DescribeIndex response. The rich configuration
// blocks are emitted verbatim as raw JSON so they round-trip without drift, and
// are omitted when unset so an IaC plan sees an absent block rather than null.
type indexDescriptionJSON struct {
	ID                                string          `json:"Id"`
	Name                              string          `json:"Name"`
	Edition                           string          `json:"Edition"`
	RoleArn                           string          `json:"RoleArn"`
	Description                       string          `json:"Description,omitempty"`
	Status                            string          `json:"Status"`
	UserContextPolicy                 string          `json:"UserContextPolicy,omitempty"`
	ServerSideEncryptionConfiguration json.RawMessage `json:"ServerSideEncryptionConfiguration,omitempty"`
	CapacityUnits                     json.RawMessage `json:"CapacityUnits,omitempty"`
	DocumentMetadataConfigurations    json.RawMessage `json:"DocumentMetadataConfigurations,omitempty"`
	UserGroupResolutionConfiguration  json.RawMessage `json:"UserGroupResolutionConfiguration,omitempty"`
	UserTokenConfigurations           json.RawMessage `json:"UserTokenConfigurations,omitempty"`
	CreatedAt                         int64           `json:"CreatedAt"`
	UpdatedAt                         int64           `json:"UpdatedAt"`
}

func toIndexDescription(i *driver.Index) indexDescriptionJSON {
	return indexDescriptionJSON{
		ID:                                i.ID,
		Name:                              i.Name,
		Edition:                           i.Edition,
		RoleArn:                           i.RoleArn,
		Description:                       i.Description,
		Status:                            i.Status,
		UserContextPolicy:                 i.UserContextPolicy,
		ServerSideEncryptionConfiguration: i.ServerSideEncryptionConfiguration,
		CapacityUnits:                     i.CapacityUnits,
		DocumentMetadataConfigurations:    i.DocumentMetadataConfigurations,
		UserGroupResolutionConfiguration:  i.UserGroupResolutionConfiguration,
		UserTokenConfigurations:           i.UserTokenConfigurations,
		CreatedAt:                         epochSeconds(i.CreatedAt),
		UpdatedAt:                         epochSeconds(i.UpdatedAt),
	}
}

// indexSummaryJSON is an IndexConfigurationSummary in a ListIndices response.
type indexSummaryJSON struct {
	ID        string `json:"Id"`
	Name      string `json:"Name,omitempty"`
	Edition   string `json:"Edition,omitempty"`
	Status    string `json:"Status"`
	CreatedAt int64  `json:"CreatedAt"`
	UpdatedAt int64  `json:"UpdatedAt"`
}

func toIndexSummary(i *driver.Index) indexSummaryJSON {
	return indexSummaryJSON{
		ID:        i.ID,
		Name:      i.Name,
		Edition:   i.Edition,
		Status:    i.Status,
		CreatedAt: epochSeconds(i.CreatedAt),
		UpdatedAt: epochSeconds(i.UpdatedAt),
	}
}

// dataSourceDescriptionJSON is the DescribeDataSource response.
type dataSourceDescriptionJSON struct {
	ID                                    string          `json:"Id"`
	IndexID                               string          `json:"IndexId"`
	Name                                  string          `json:"Name"`
	Type                                  string          `json:"Type"`
	RoleArn                               string          `json:"RoleArn,omitempty"`
	Description                           string          `json:"Description,omitempty"`
	Schedule                              string          `json:"Schedule,omitempty"`
	LanguageCode                          string          `json:"LanguageCode,omitempty"`
	Status                                string          `json:"Status"`
	Configuration                         json.RawMessage `json:"Configuration,omitempty"`
	VpcConfiguration                      json.RawMessage `json:"VpcConfiguration,omitempty"`
	CustomDocumentEnrichmentConfiguration json.RawMessage `json:"CustomDocumentEnrichmentConfiguration,omitempty"`
	CreatedAt                             int64           `json:"CreatedAt"`
	UpdatedAt                             int64           `json:"UpdatedAt"`
}

func toDataSourceDescription(d *driver.DataSource) dataSourceDescriptionJSON {
	return dataSourceDescriptionJSON{
		ID:                                    d.ID,
		IndexID:                               d.IndexID,
		Name:                                  d.Name,
		Type:                                  d.Type,
		RoleArn:                               d.RoleArn,
		Description:                           d.Description,
		Schedule:                              d.Schedule,
		LanguageCode:                          d.LanguageCode,
		Status:                                d.Status,
		Configuration:                         d.Configuration,
		VpcConfiguration:                      d.VpcConfiguration,
		CustomDocumentEnrichmentConfiguration: d.CustomDocumentEnrichmentConfiguration,
		CreatedAt:                             epochSeconds(d.CreatedAt),
		UpdatedAt:                             epochSeconds(d.UpdatedAt),
	}
}

// dataSourceSummaryJSON is a DataSourceSummary in a ListDataSources response.
type dataSourceSummaryJSON struct {
	ID           string `json:"Id"`
	Name         string `json:"Name,omitempty"`
	Type         string `json:"Type,omitempty"`
	LanguageCode string `json:"LanguageCode,omitempty"`
	Status       string `json:"Status"`
	CreatedAt    int64  `json:"CreatedAt"`
	UpdatedAt    int64  `json:"UpdatedAt"`
}

func toDataSourceSummary(d *driver.DataSource) dataSourceSummaryJSON {
	return dataSourceSummaryJSON{
		ID:           d.ID,
		Name:         d.Name,
		Type:         d.Type,
		LanguageCode: d.LanguageCode,
		Status:       d.Status,
		CreatedAt:    epochSeconds(d.CreatedAt),
		UpdatedAt:    epochSeconds(d.UpdatedAt),
	}
}

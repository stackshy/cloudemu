package cloudtrail

import (
	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// trailJSON is the CloudTrail Trail wire shape.
type trailJSON struct {
	Name                       string `json:"Name,omitempty"`
	TrailARN                   string `json:"TrailARN,omitempty"`
	S3BucketName               string `json:"S3BucketName,omitempty"`
	S3KeyPrefix                string `json:"S3KeyPrefix,omitempty"`
	SnsTopicName               string `json:"SnsTopicName,omitempty"`
	SnsTopicARN                string `json:"SnsTopicARN,omitempty"`
	IncludeGlobalServiceEvents bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         bool   `json:"IsMultiRegionTrail"`
	IsOrganizationTrail        bool   `json:"IsOrganizationTrail"`
	HomeRegion                 string `json:"HomeRegion,omitempty"`
	LogFileValidationEnabled   bool   `json:"LogFileValidationEnabled"`
	CloudWatchLogsLogGroupArn  string `json:"CloudWatchLogsLogGroupArn,omitempty"`
	CloudWatchLogsRoleArn      string `json:"CloudWatchLogsRoleArn,omitempty"`
	KmsKeyID                   string `json:"KmsKeyId,omitempty"`
	HasCustomEventSelectors    bool   `json:"HasCustomEventSelectors"`
	HasInsightSelectors        bool   `json:"HasInsightSelectors"`
}

func trailToWire(t *ctdriver.Trail) trailJSON {
	return trailJSON{
		Name:                       t.Name,
		TrailARN:                   t.TrailARN,
		S3BucketName:               t.S3BucketName,
		S3KeyPrefix:                t.S3KeyPrefix,
		SnsTopicName:               t.SNSTopicName,
		SnsTopicARN:                t.SNSTopicARN,
		IncludeGlobalServiceEvents: t.IncludeGlobalServiceEvents,
		IsMultiRegionTrail:         t.IsMultiRegionTrail,
		IsOrganizationTrail:        t.IsOrganizationTrail,
		HomeRegion:                 t.HomeRegion,
		LogFileValidationEnabled:   t.LogFileValidationEnabled,
		CloudWatchLogsLogGroupArn:  t.CloudWatchLogsLogGroupARN,
		CloudWatchLogsRoleArn:      t.CloudWatchLogsRoleARN,
		KmsKeyID:                   t.KMSKeyID,
		HasCustomEventSelectors:    t.HasCustomEventSelectors,
		HasInsightSelectors:        t.HasInsightSelectors,
	}
}

// edsJSON is the EventDataStore wire shape.
type edsJSON struct {
	Name                         string                      `json:"Name,omitempty"`
	EventDataStoreArn            string                      `json:"EventDataStoreArn,omitempty"`
	Status                       string                      `json:"Status,omitempty"`
	BillingMode                  string                      `json:"BillingMode,omitempty"`
	RetentionPeriod              int32                       `json:"RetentionPeriod,omitempty"`
	MultiRegionEnabled           bool                        `json:"MultiRegionEnabled"`
	OrganizationEnabled          bool                        `json:"OrganizationEnabled"`
	TerminationProtectionEnabled bool                        `json:"TerminationProtectionEnabled"`
	KmsKeyID                     string                      `json:"KmsKeyId,omitempty"`
	AdvancedEventSelectors       []advancedEventSelectorJSON `json:"AdvancedEventSelectors,omitempty"`
	CreatedTimestamp             *float64                    `json:"CreatedTimestamp,omitempty"`
	UpdatedTimestamp             *float64                    `json:"UpdatedTimestamp,omitempty"`
	TagsList                     []tag                       `json:"TagsList,omitempty"`
}

func edsToWire(e *ctdriver.EventDataStore) edsJSON {
	return edsJSON{
		Name:                         e.Name,
		EventDataStoreArn:            e.ARN,
		Status:                       e.Status,
		BillingMode:                  e.BillingMode,
		RetentionPeriod:              e.RetentionPeriod,
		MultiRegionEnabled:           e.MultiRegionEnabled,
		OrganizationEnabled:          e.OrganizationEnabled,
		TerminationProtectionEnabled: e.TerminationProtectionEnabled,
		KmsKeyID:                     e.KMSKeyID,
		AdvancedEventSelectors:       advSelectorsToWire(e.AdvancedEventSelectors),
		CreatedTimestamp:             epochOrNil(e.CreatedAt),
		UpdatedTimestamp:             epochOrNil(e.UpdatedAt),
		TagsList:                     mapToTags(e.Tags),
	}
}

// channelJSON is the Channel wire shape used in Get/List responses.
type channelJSON struct {
	Name         string            `json:"Name,omitempty"`
	ChannelArn   string            `json:"ChannelArn,omitempty"`
	Source       string            `json:"Source,omitempty"`
	Destinations []destinationJSON `json:"Destinations,omitempty"`
}

func channelToWire(c *ctdriver.Channel) channelJSON {
	return channelJSON{
		Name:         c.Name,
		ChannelArn:   c.ARN,
		Source:       c.Source,
		Destinations: destinationsToWire(c.Destinations),
	}
}

// dashboardJSON is the Dashboard wire shape used in List responses.
type dashboardJSON struct {
	Name         string `json:"DashboardName,omitempty"`
	DashboardArn string `json:"DashboardArn,omitempty"`
	Type         string `json:"Type,omitempty"`
	Status       string `json:"Status,omitempty"`
}

func dashboardToWire(d *ctdriver.Dashboard) dashboardJSON {
	return dashboardJSON{Name: d.Name, DashboardArn: d.ARN, Type: d.Type, Status: d.Status}
}

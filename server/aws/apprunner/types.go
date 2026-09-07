package apprunner

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// tagJSON is the wire shape of an App Runner tag.
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

const millisPerSecond = 1000.0

// epochSeconds renders a timestamp as JSON-1.0 epoch seconds, or nil for the
// zero time so an unset timestamp reads back as null.
func epochSeconds(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return float64(t.UTC().UnixMilli()) / millisPerSecond
}

type sourceCodeVersionJSON struct {
	Type  string `json:"Type,omitempty"`
	Value string `json:"Value,omitempty"`
}

type codeConfigurationValuesJSON struct {
	Runtime                     string            `json:"Runtime,omitempty"`
	BuildCommand                string            `json:"BuildCommand,omitempty"`
	StartCommand                string            `json:"StartCommand,omitempty"`
	Port                        string            `json:"Port,omitempty"`
	RuntimeEnvironmentVariables map[string]string `json:"RuntimeEnvironmentVariables,omitempty"`
	RuntimeEnvironmentSecrets   map[string]string `json:"RuntimeEnvironmentSecrets,omitempty"`
}

type codeConfigurationJSON struct {
	ConfigurationSource     string                       `json:"ConfigurationSource,omitempty"`
	CodeConfigurationValues *codeConfigurationValuesJSON `json:"CodeConfigurationValues,omitempty"`
}

type codeRepositoryJSON struct {
	RepositoryURL     string                 `json:"RepositoryUrl,omitempty"`
	SourceCodeVersion *sourceCodeVersionJSON `json:"SourceCodeVersion,omitempty"`
	CodeConfiguration *codeConfigurationJSON `json:"CodeConfiguration,omitempty"`
	SourceDirectory   string                 `json:"SourceDirectory,omitempty"`
}

type imageConfigurationJSON struct {
	Port                        string            `json:"Port,omitempty"`
	StartCommand                string            `json:"StartCommand,omitempty"`
	RuntimeEnvironmentVariables map[string]string `json:"RuntimeEnvironmentVariables,omitempty"`
	RuntimeEnvironmentSecrets   map[string]string `json:"RuntimeEnvironmentSecrets,omitempty"`
}

type imageRepositoryJSON struct {
	ImageIdentifier     string                  `json:"ImageIdentifier,omitempty"`
	ImageConfiguration  *imageConfigurationJSON `json:"ImageConfiguration,omitempty"`
	ImageRepositoryType string                  `json:"ImageRepositoryType,omitempty"`
}

type authenticationConfigurationJSON struct {
	ConnectionArn string `json:"ConnectionArn,omitempty"`
	AccessRoleArn string `json:"AccessRoleArn,omitempty"`
}

type sourceConfigurationJSON struct {
	CodeRepository              *codeRepositoryJSON              `json:"CodeRepository,omitempty"`
	ImageRepository             *imageRepositoryJSON             `json:"ImageRepository,omitempty"`
	AutoDeploymentsEnabled      *bool                            `json:"AutoDeploymentsEnabled,omitempty"`
	AuthenticationConfiguration *authenticationConfigurationJSON `json:"AuthenticationConfiguration,omitempty"`
}

type instanceConfigurationJSON struct {
	CPU             string `json:"Cpu,omitempty"`
	Memory          string `json:"Memory,omitempty"`
	InstanceRoleArn string `json:"InstanceRoleArn,omitempty"`
}

type healthCheckConfigurationJSON struct {
	Protocol           string `json:"Protocol,omitempty"`
	Path               string `json:"Path,omitempty"`
	Interval           *int32 `json:"Interval,omitempty"`
	Timeout            *int32 `json:"Timeout,omitempty"`
	HealthyThreshold   *int32 `json:"HealthyThreshold,omitempty"`
	UnhealthyThreshold *int32 `json:"UnhealthyThreshold,omitempty"`
}

type egressConfigurationJSON struct {
	EgressType      string `json:"EgressType,omitempty"`
	VpcConnectorArn string `json:"VpcConnectorArn,omitempty"`
}

type ingressConfigurationJSON struct {
	IsPubliclyAccessible *bool `json:"IsPubliclyAccessible,omitempty"`
}

type networkConfigurationJSON struct {
	EgressConfiguration  *egressConfigurationJSON  `json:"EgressConfiguration,omitempty"`
	IngressConfiguration *ingressConfigurationJSON `json:"IngressConfiguration,omitempty"`
	IPAddressType        string                    `json:"IpAddressType,omitempty"`
}

type serviceObservabilityConfigurationJSON struct {
	ObservabilityEnabled          *bool  `json:"ObservabilityEnabled,omitempty"`
	ObservabilityConfigurationArn string `json:"ObservabilityConfigurationArn,omitempty"`
}

type encryptionConfigurationJSON struct {
	KmsKey string `json:"KmsKey,omitempty"`
}

type autoScalingConfigurationSummaryJSON struct {
	AutoScalingConfigurationArn      string `json:"AutoScalingConfigurationArn,omitempty"`
	AutoScalingConfigurationName     string `json:"AutoScalingConfigurationName,omitempty"`
	AutoScalingConfigurationRevision int32  `json:"AutoScalingConfigurationRevision,omitempty"`
}

// serviceJSON is the wire shape of a Service, shared by every service response.
type serviceJSON struct {
	ServiceName                     string                                 `json:"ServiceName"`
	ServiceID                       string                                 `json:"ServiceId"`
	ServiceArn                      string                                 `json:"ServiceArn"`
	ServiceURL                      string                                 `json:"ServiceUrl,omitempty"`
	Status                          string                                 `json:"Status"`
	CreatedAt                       any                                    `json:"CreatedAt"`
	UpdatedAt                       any                                    `json:"UpdatedAt"`
	DeletedAt                       any                                    `json:"DeletedAt,omitempty"`
	SourceConfiguration             *sourceConfigurationJSON               `json:"SourceConfiguration,omitempty"`
	InstanceConfiguration           *instanceConfigurationJSON             `json:"InstanceConfiguration,omitempty"`
	HealthCheckConfiguration        *healthCheckConfigurationJSON          `json:"HealthCheckConfiguration,omitempty"`
	NetworkConfiguration            *networkConfigurationJSON              `json:"NetworkConfiguration,omitempty"`
	ObservabilityConfiguration      *serviceObservabilityConfigurationJSON `json:"ObservabilityConfiguration,omitempty"`
	EncryptionConfiguration         *encryptionConfigurationJSON           `json:"EncryptionConfiguration,omitempty"`
	AutoScalingConfigurationSummary *autoScalingConfigurationSummaryJSON   `json:"AutoScalingConfigurationSummary,omitempty"`
}

// operationSummaryJSON is the wire shape of an OperationSummary.
type operationSummaryJSON struct {
	ID        string `json:"Id"`
	Type      string `json:"Type"`
	Status    string `json:"Status"`
	TargetArn string `json:"TargetArn,omitempty"`
	StartedAt any    `json:"StartedAt,omitempty"`
	EndedAt   any    `json:"EndedAt,omitempty"`
	UpdatedAt any    `json:"UpdatedAt,omitempty"`
}

package apprunner

import "github.com/stackshy/cloudemu/v2/services/apprunner/driver"

func copyTags(in []driver.Tag) []driver.Tag {
	if in == nil {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyInt32(in *int32) *int32 {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyCodeConfigValues(in *driver.CodeConfigurationValues) *driver.CodeConfigurationValues {
	if in == nil {
		return nil
	}

	out := *in
	out.RuntimeEnvironmentVariables = copyStringMap(in.RuntimeEnvironmentVariables)
	out.RuntimeEnvironmentSecrets = copyStringMap(in.RuntimeEnvironmentSecrets)

	return &out
}

func copyCodeRepository(in *driver.CodeRepository) *driver.CodeRepository {
	if in == nil {
		return nil
	}

	out := *in

	if in.SourceCodeVersion != nil {
		scv := *in.SourceCodeVersion
		out.SourceCodeVersion = &scv
	}

	if in.CodeConfiguration != nil {
		cc := *in.CodeConfiguration
		cc.CodeConfigurationValues = copyCodeConfigValues(in.CodeConfiguration.CodeConfigurationValues)
		out.CodeConfiguration = &cc
	}

	return &out
}

func copyImageRepository(in *driver.ImageRepository) *driver.ImageRepository {
	if in == nil {
		return nil
	}

	out := *in

	if in.ImageConfiguration != nil {
		ic := *in.ImageConfiguration
		ic.RuntimeEnvironmentVariables = copyStringMap(in.ImageConfiguration.RuntimeEnvironmentVariables)
		ic.RuntimeEnvironmentSecrets = copyStringMap(in.ImageConfiguration.RuntimeEnvironmentSecrets)
		out.ImageConfiguration = &ic
	}

	return &out
}

func copySourceConfiguration(in *driver.SourceConfiguration) *driver.SourceConfiguration {
	if in == nil {
		return nil
	}

	out := driver.SourceConfiguration{
		CodeRepository:         copyCodeRepository(in.CodeRepository),
		ImageRepository:        copyImageRepository(in.ImageRepository),
		AutoDeploymentsEnabled: copyBool(in.AutoDeploymentsEnabled),
	}

	if in.AuthenticationConfiguration != nil {
		ac := *in.AuthenticationConfiguration
		out.AuthenticationConfiguration = &ac
	}

	return &out
}

func copyInstanceConfiguration(in *driver.InstanceConfiguration) *driver.InstanceConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyHealthCheck(in *driver.HealthCheckConfiguration) *driver.HealthCheckConfiguration {
	if in == nil {
		return nil
	}

	return &driver.HealthCheckConfiguration{
		Protocol:           in.Protocol,
		Path:               in.Path,
		Interval:           copyInt32(in.Interval),
		Timeout:            copyInt32(in.Timeout),
		HealthyThreshold:   copyInt32(in.HealthyThreshold),
		UnhealthyThreshold: copyInt32(in.UnhealthyThreshold),
	}
}

func copyNetworkConfiguration(in *driver.NetworkConfiguration) *driver.NetworkConfiguration {
	if in == nil {
		return nil
	}

	out := driver.NetworkConfiguration{IPAddressType: in.IPAddressType}

	if in.EgressConfiguration != nil {
		ec := *in.EgressConfiguration
		out.EgressConfiguration = &ec
	}

	if in.IngressConfiguration != nil {
		out.IngressConfiguration = &driver.IngressConfiguration{
			IsPubliclyAccessible: copyBool(in.IngressConfiguration.IsPubliclyAccessible),
		}
	}

	return &out
}

func copyObservabilityConfig(in *driver.ServiceObservabilityConfiguration) *driver.ServiceObservabilityConfiguration {
	if in == nil {
		return nil
	}

	return &driver.ServiceObservabilityConfiguration{
		ObservabilityEnabled:          copyBool(in.ObservabilityEnabled),
		ObservabilityConfigurationArn: in.ObservabilityConfigurationArn,
	}
}

func copyEncryptionConfig(in *driver.EncryptionConfiguration) *driver.EncryptionConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyAutoScalingSummary(in *driver.AutoScalingConfigurationSummary) *driver.AutoScalingConfigurationSummary {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyOperations(in []driver.Operation) []driver.Operation {
	if in == nil {
		return nil
	}

	return append([]driver.Operation(nil), in...)
}

// copyService returns an alias-free copy of a service, so callers cannot mutate
// stored state through the result.
func copyService(s *driver.Service) driver.Service {
	out := *s
	out.SourceConfiguration = copySourceConfiguration(s.SourceConfiguration)
	out.InstanceConfiguration = copyInstanceConfiguration(s.InstanceConfiguration)
	out.HealthCheckConfiguration = copyHealthCheck(s.HealthCheckConfiguration)
	out.NetworkConfiguration = copyNetworkConfiguration(s.NetworkConfiguration)
	out.ObservabilityConfiguration = copyObservabilityConfig(s.ObservabilityConfiguration)
	out.EncryptionConfiguration = copyEncryptionConfig(s.EncryptionConfiguration)
	out.AutoScalingConfigurationSummary = copyAutoScalingSummary(s.AutoScalingConfigurationSummary)
	out.Tags = copyTags(s.Tags)
	out.Operations = copyOperations(s.Operations)

	return out
}

func copyAutoScaling(c *driver.AutoScalingConfiguration) driver.AutoScalingConfiguration {
	out := *c
	out.Tags = copyTags(c.Tags)

	return out
}

func copyConnection(c *driver.Connection) driver.Connection {
	out := *c
	out.Tags = copyTags(c.Tags)

	return out
}

func copyVpcConnector(c *driver.VpcConnector) driver.VpcConnector {
	out := *c
	out.Subnets = copyStrings(c.Subnets)
	out.SecurityGroups = copyStrings(c.SecurityGroups)
	out.Tags = copyTags(c.Tags)

	return out
}

func copyObservability(c *driver.ObservabilityConfiguration) driver.ObservabilityConfiguration {
	out := *c
	out.Tags = copyTags(c.Tags)

	if c.TraceConfiguration != nil {
		tc := *c.TraceConfiguration
		out.TraceConfiguration = &tc
	}

	return out
}

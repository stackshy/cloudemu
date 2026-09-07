package apprunner

import "github.com/stackshy/cloudemu/v2/services/apprunner/driver"

func sourceConfigFromWire(in *sourceConfigurationJSON) *driver.SourceConfiguration {
	if in == nil {
		return nil
	}

	return &driver.SourceConfiguration{
		CodeRepository:              codeRepoFromWire(in.CodeRepository),
		ImageRepository:             imageRepoFromWire(in.ImageRepository),
		AutoDeploymentsEnabled:      in.AutoDeploymentsEnabled,
		AuthenticationConfiguration: authFromWire(in.AuthenticationConfiguration),
	}
}

func authFromWire(in *authenticationConfigurationJSON) *driver.AuthenticationConfiguration {
	if in == nil {
		return nil
	}

	return &driver.AuthenticationConfiguration{ConnectionArn: in.ConnectionArn, AccessRoleArn: in.AccessRoleArn}
}

func codeRepoFromWire(in *codeRepositoryJSON) *driver.CodeRepository {
	if in == nil {
		return nil
	}

	out := &driver.CodeRepository{RepositoryURL: in.RepositoryURL, SourceDirectory: in.SourceDirectory}

	if in.SourceCodeVersion != nil {
		out.SourceCodeVersion = &driver.SourceCodeVersion{Type: in.SourceCodeVersion.Type, Value: in.SourceCodeVersion.Value}
	}

	if in.CodeConfiguration != nil {
		out.CodeConfiguration = &driver.CodeConfiguration{
			ConfigurationSource:     in.CodeConfiguration.ConfigurationSource,
			CodeConfigurationValues: codeValuesFromWire(in.CodeConfiguration.CodeConfigurationValues),
		}
	}

	return out
}

func codeValuesFromWire(in *codeConfigurationValuesJSON) *driver.CodeConfigurationValues {
	if in == nil {
		return nil
	}

	return &driver.CodeConfigurationValues{
		Runtime:                     in.Runtime,
		BuildCommand:                in.BuildCommand,
		StartCommand:                in.StartCommand,
		Port:                        in.Port,
		RuntimeEnvironmentVariables: in.RuntimeEnvironmentVariables,
		RuntimeEnvironmentSecrets:   in.RuntimeEnvironmentSecrets,
	}
}

func imageRepoFromWire(in *imageRepositoryJSON) *driver.ImageRepository {
	if in == nil {
		return nil
	}

	out := &driver.ImageRepository{ImageIdentifier: in.ImageIdentifier, ImageRepositoryType: in.ImageRepositoryType}

	if in.ImageConfiguration != nil {
		out.ImageConfiguration = &driver.ImageConfiguration{
			Port:                        in.ImageConfiguration.Port,
			StartCommand:                in.ImageConfiguration.StartCommand,
			RuntimeEnvironmentVariables: in.ImageConfiguration.RuntimeEnvironmentVariables,
			RuntimeEnvironmentSecrets:   in.ImageConfiguration.RuntimeEnvironmentSecrets,
		}
	}

	return out
}

func instanceConfigFromWire(in *instanceConfigurationJSON) *driver.InstanceConfiguration {
	if in == nil {
		return nil
	}

	return &driver.InstanceConfiguration{CPU: in.CPU, Memory: in.Memory, InstanceRoleArn: in.InstanceRoleArn}
}

func healthCheckFromWire(in *healthCheckConfigurationJSON) *driver.HealthCheckConfiguration {
	if in == nil {
		return nil
	}

	return &driver.HealthCheckConfiguration{
		Protocol:           in.Protocol,
		Path:               in.Path,
		Interval:           in.Interval,
		Timeout:            in.Timeout,
		HealthyThreshold:   in.HealthyThreshold,
		UnhealthyThreshold: in.UnhealthyThreshold,
	}
}

func networkConfigFromWire(in *networkConfigurationJSON) *driver.NetworkConfiguration {
	if in == nil {
		return nil
	}

	out := &driver.NetworkConfiguration{IPAddressType: in.IPAddressType}

	if in.EgressConfiguration != nil {
		out.EgressConfiguration = &driver.EgressConfiguration{
			EgressType:      in.EgressConfiguration.EgressType,
			VpcConnectorArn: in.EgressConfiguration.VpcConnectorArn,
		}
	}

	if in.IngressConfiguration != nil {
		out.IngressConfiguration = &driver.IngressConfiguration{
			IsPubliclyAccessible: in.IngressConfiguration.IsPubliclyAccessible,
		}
	}

	return out
}

func serviceObsFromWire(in *serviceObservabilityConfigurationJSON) *driver.ServiceObservabilityConfiguration {
	if in == nil {
		return nil
	}

	return &driver.ServiceObservabilityConfiguration{
		ObservabilityEnabled:          in.ObservabilityEnabled,
		ObservabilityConfigurationArn: in.ObservabilityConfigurationArn,
	}
}

func encryptionFromWire(in *encryptionConfigurationJSON) *driver.EncryptionConfiguration {
	if in == nil {
		return nil
	}

	return &driver.EncryptionConfiguration{KmsKey: in.KmsKey}
}

// serviceToWire renders a driver.Service as its wire shape.
func serviceToWire(s *driver.Service) serviceJSON {
	return serviceJSON{
		ServiceName:                     s.ServiceName,
		ServiceID:                       s.ServiceID,
		ServiceArn:                      s.ServiceArn,
		ServiceURL:                      s.ServiceURL,
		Status:                          s.Status,
		CreatedAt:                       epochSeconds(s.CreatedAt),
		UpdatedAt:                       epochSeconds(s.UpdatedAt),
		DeletedAt:                       epochSeconds(s.DeletedAt),
		SourceConfiguration:             sourceConfigToWire(s.SourceConfiguration),
		InstanceConfiguration:           instanceConfigToWire(s.InstanceConfiguration),
		HealthCheckConfiguration:        healthCheckToWire(s.HealthCheckConfiguration),
		NetworkConfiguration:            networkConfigToWire(s.NetworkConfiguration),
		ObservabilityConfiguration:      serviceObsToWire(s.ObservabilityConfiguration),
		EncryptionConfiguration:         encryptionToWire(s.EncryptionConfiguration),
		AutoScalingConfigurationSummary: autoScalingSummaryToWire(s.AutoScalingConfigurationSummary),
	}
}

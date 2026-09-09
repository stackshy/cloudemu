package apprunner

import "github.com/stackshy/cloudemu/v2/services/apprunner/driver"

func sourceConfigToWire(in *driver.SourceConfiguration) *sourceConfigurationJSON {
	if in == nil {
		return nil
	}

	out := &sourceConfigurationJSON{
		CodeRepository:         codeRepoToWire(in.CodeRepository),
		ImageRepository:        imageRepoToWire(in.ImageRepository),
		AutoDeploymentsEnabled: in.AutoDeploymentsEnabled,
	}

	if in.AuthenticationConfiguration != nil {
		out.AuthenticationConfiguration = &authenticationConfigurationJSON{
			ConnectionArn: in.AuthenticationConfiguration.ConnectionArn,
			AccessRoleArn: in.AuthenticationConfiguration.AccessRoleArn,
		}
	}

	return out
}

func codeRepoToWire(in *driver.CodeRepository) *codeRepositoryJSON {
	if in == nil {
		return nil
	}

	out := &codeRepositoryJSON{RepositoryURL: in.RepositoryURL, SourceDirectory: in.SourceDirectory}

	if in.SourceCodeVersion != nil {
		out.SourceCodeVersion = &sourceCodeVersionJSON{Type: in.SourceCodeVersion.Type, Value: in.SourceCodeVersion.Value}
	}

	if in.CodeConfiguration != nil {
		out.CodeConfiguration = &codeConfigurationJSON{
			ConfigurationSource:     in.CodeConfiguration.ConfigurationSource,
			CodeConfigurationValues: codeValuesToWire(in.CodeConfiguration.CodeConfigurationValues),
		}
	}

	return out
}

func codeValuesToWire(in *driver.CodeConfigurationValues) *codeConfigurationValuesJSON {
	if in == nil {
		return nil
	}

	return &codeConfigurationValuesJSON{
		Runtime:                     in.Runtime,
		BuildCommand:                in.BuildCommand,
		StartCommand:                in.StartCommand,
		Port:                        in.Port,
		RuntimeEnvironmentVariables: in.RuntimeEnvironmentVariables,
		RuntimeEnvironmentSecrets:   in.RuntimeEnvironmentSecrets,
	}
}

func imageRepoToWire(in *driver.ImageRepository) *imageRepositoryJSON {
	if in == nil {
		return nil
	}

	out := &imageRepositoryJSON{ImageIdentifier: in.ImageIdentifier, ImageRepositoryType: in.ImageRepositoryType}

	if in.ImageConfiguration != nil {
		out.ImageConfiguration = &imageConfigurationJSON{
			Port:                        in.ImageConfiguration.Port,
			StartCommand:                in.ImageConfiguration.StartCommand,
			RuntimeEnvironmentVariables: in.ImageConfiguration.RuntimeEnvironmentVariables,
			RuntimeEnvironmentSecrets:   in.ImageConfiguration.RuntimeEnvironmentSecrets,
		}
	}

	return out
}

func instanceConfigToWire(in *driver.InstanceConfiguration) *instanceConfigurationJSON {
	if in == nil {
		return nil
	}

	return &instanceConfigurationJSON{CPU: in.CPU, Memory: in.Memory, InstanceRoleArn: in.InstanceRoleArn}
}

func healthCheckToWire(in *driver.HealthCheckConfiguration) *healthCheckConfigurationJSON {
	if in == nil {
		return nil
	}

	return &healthCheckConfigurationJSON{
		Protocol:           in.Protocol,
		Path:               in.Path,
		Interval:           in.Interval,
		Timeout:            in.Timeout,
		HealthyThreshold:   in.HealthyThreshold,
		UnhealthyThreshold: in.UnhealthyThreshold,
	}
}

func networkConfigToWire(in *driver.NetworkConfiguration) *networkConfigurationJSON {
	if in == nil {
		return nil
	}

	out := &networkConfigurationJSON{IPAddressType: in.IPAddressType}

	if in.EgressConfiguration != nil {
		out.EgressConfiguration = &egressConfigurationJSON{
			EgressType:      in.EgressConfiguration.EgressType,
			VpcConnectorArn: in.EgressConfiguration.VpcConnectorArn,
		}
	}

	if in.IngressConfiguration != nil {
		out.IngressConfiguration = &ingressConfigurationJSON{
			IsPubliclyAccessible: in.IngressConfiguration.IsPubliclyAccessible,
		}
	}

	return out
}

func serviceObsToWire(in *driver.ServiceObservabilityConfiguration) *serviceObservabilityConfigurationJSON {
	if in == nil {
		return nil
	}

	return &serviceObservabilityConfigurationJSON{
		ObservabilityEnabled:          in.ObservabilityEnabled,
		ObservabilityConfigurationArn: in.ObservabilityConfigurationArn,
	}
}

func encryptionToWire(in *driver.EncryptionConfiguration) *encryptionConfigurationJSON {
	if in == nil {
		return nil
	}

	return &encryptionConfigurationJSON{KmsKey: in.KmsKey}
}

func autoScalingSummaryToWire(in *driver.AutoScalingConfigurationSummary) *autoScalingConfigurationSummaryJSON {
	if in == nil {
		return nil
	}

	return &autoScalingConfigurationSummaryJSON{
		AutoScalingConfigurationArn:      in.AutoScalingConfigurationArn,
		AutoScalingConfigurationName:     in.AutoScalingConfigurationName,
		AutoScalingConfigurationRevision: in.AutoScalingConfigurationRevision,
	}
}

// operationToWire renders a driver.Operation as its wire shape.
func operationToWire(op *driver.Operation) operationSummaryJSON {
	return operationSummaryJSON{
		ID:        op.ID,
		Type:      op.Type,
		Status:    op.Status,
		TargetArn: op.TargetArn,
		StartedAt: epochSeconds(op.StartedAt),
		EndedAt:   epochSeconds(op.EndedAt),
		UpdatedAt: epochSeconds(op.UpdatedAt),
	}
}

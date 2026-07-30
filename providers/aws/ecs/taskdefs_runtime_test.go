package ecs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// richContainerDef builds a container definition exercising every runtime field
// the Wave 4a model adds.
func richContainerDef() driver.ContainerDefinition {
	return driver.ContainerDefinition{
		Name:              "app",
		Image:             "nginx:latest",
		CPU:               256,
		Memory:            512,
		MemoryReservation: 256,
		Essential:         true,
		PortMappings: []driver.PortMapping{
			{ContainerPort: 8080, HostPort: 8080, Protocol: "tcp", Name: "http", AppProtocol: "http"},
		},
		Command:          []string{"nginx", "-g", "daemon off;"},
		EntryPoint:       []string{"/entry.sh"},
		Environment:      []driver.KeyValue{{Name: "ENV", Value: "prod"}},
		Secrets:          []driver.Secret{{Name: "TOKEN", ValueFrom: "arn:aws:ssm:::parameter/token"}},
		EnvironmentFiles: []driver.EnvironmentFile{{Value: "arn:aws:s3:::b/env", Type: "s3"}},
		HealthCheck: &driver.HealthCheck{
			Command:  []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"},
			Interval: 30, Timeout: 5, Retries: 3, StartPeriod: 10,
		},
		DependsOn:   []driver.ContainerDependency{{ContainerName: "sidecar", Condition: "HEALTHY"}},
		MountPoints: []driver.MountPoint{{SourceVolume: "data", ContainerPath: "/data", ReadOnly: true}},
		VolumesFrom: []driver.VolumeFrom{{SourceContainer: "seed", ReadOnly: true}},
		LogConfiguration: &driver.LogConfiguration{
			LogDriver:     "awslogs",
			Options:       map[string]string{"awslogs-group": "/ecs/app"},
			SecretOptions: []driver.Secret{{Name: "LOG_TOKEN", ValueFrom: "arn:aws:ssm:::parameter/log"}},
		},
		Ulimits:                []driver.Ulimit{{Name: "nofile", SoftLimit: 1024, HardLimit: 2048}},
		ResourceRequirements:   []driver.ResourceRequirement{{Value: "1", Type: "GPU"}},
		LinuxParameters:        json.RawMessage(`{"initProcessEnabled":true}`),
		FirelensConfiguration:  json.RawMessage(`{"type":"fluentbit"}`),
		StopTimeout:            30,
		StartTimeout:           60,
		User:                   "1000:1000",
		WorkingDirectory:       "/app",
		Hostname:               "app-host",
		Privileged:             true,
		ReadonlyRootFilesystem: true,
	}
}

func TestRegisterTaskDefinitionRuntimeFieldsRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	in := driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{richContainerDef()},
		CPU:                  "256",
		Memory:               "512",
		Volumes: []driver.Volume{{
			Name:                      "data",
			Host:                      &driver.HostVolumeProperties{SourcePath: "/mnt/data"},
			EFSVolumeConfiguration:    json.RawMessage(`{"fileSystemId":"fs-123"}`),
			DockerVolumeConfiguration: json.RawMessage(`{"scope":"shared"}`),
		}},
		EphemeralStorage:      &driver.EphemeralStorage{SizeInGiB: 40},
		PidMode:               "task",
		IpcMode:               "host",
		RuntimePlatform:       &driver.RuntimePlatform{CPUArchitecture: "ARM64", OperatingSystemFamily: "LINUX"},
		ProxyConfiguration:    &driver.ProxyConfiguration{Type: "APPMESH", ContainerName: "envoy", Properties: []driver.KeyValue{{Name: "IgnoredUID", Value: "1337"}}},
		PlacementConstraints:  []driver.PlacementConstraint{{Type: "memberOf", Expression: "attribute:ecs.instance-type == t3.micro"}},
		InferenceAccelerators: []driver.InferenceAccelerator{{DeviceName: "dev1", DeviceType: "eia2.medium"}},
		EnableFaultInjection:  true,
	}

	reg, err := m.RegisterTaskDefinition(ctx, in)
	require.NoError(t, err)

	td, err := m.DescribeTaskDefinition(ctx, reg.ARN)
	require.NoError(t, err)
	require.Len(t, td.ContainerDefinitions, 1)

	c := td.ContainerDefinitions[0]
	assert.Equal(t, 256, c.MemoryReservation)
	assert.Equal(t, "http", c.PortMappings[0].Name)
	assert.Equal(t, "http", c.PortMappings[0].AppProtocol)
	assert.Equal(t, []string{"/entry.sh"}, c.EntryPoint)
	assert.Equal(t, "TOKEN", c.Secrets[0].Name)
	assert.Equal(t, "s3", c.EnvironmentFiles[0].Type)
	require.NotNil(t, c.HealthCheck)
	assert.Equal(t, 3, c.HealthCheck.Retries)
	assert.Equal(t, "HEALTHY", c.DependsOn[0].Condition)
	assert.Equal(t, "/data", c.MountPoints[0].ContainerPath)
	assert.Equal(t, "seed", c.VolumesFrom[0].SourceContainer)
	require.NotNil(t, c.LogConfiguration)
	assert.Equal(t, "awslogs", c.LogConfiguration.LogDriver)
	assert.Equal(t, "/ecs/app", c.LogConfiguration.Options["awslogs-group"])
	assert.Equal(t, "nofile", c.Ulimits[0].Name)
	assert.Equal(t, "GPU", c.ResourceRequirements[0].Type)
	assert.JSONEq(t, `{"initProcessEnabled":true}`, string(c.LinuxParameters))
	assert.JSONEq(t, `{"type":"fluentbit"}`, string(c.FirelensConfiguration))
	assert.Equal(t, 30, c.StopTimeout)
	assert.Equal(t, 60, c.StartTimeout)
	assert.Equal(t, "1000:1000", c.User)
	assert.Equal(t, "/app", c.WorkingDirectory)
	assert.Equal(t, "app-host", c.Hostname)
	assert.True(t, c.Privileged)
	assert.True(t, c.ReadonlyRootFilesystem)

	// Task-level runtime fields.
	require.Len(t, td.Volumes, 1)
	require.NotNil(t, td.Volumes[0].Host)
	assert.Equal(t, "/mnt/data", td.Volumes[0].Host.SourcePath)
	assert.JSONEq(t, `{"fileSystemId":"fs-123"}`, string(td.Volumes[0].EFSVolumeConfiguration))
	assert.JSONEq(t, `{"scope":"shared"}`, string(td.Volumes[0].DockerVolumeConfiguration))
	require.NotNil(t, td.EphemeralStorage)
	assert.Equal(t, 40, td.EphemeralStorage.SizeInGiB)
	assert.Equal(t, "task", td.PidMode)
	assert.Equal(t, "host", td.IpcMode)
	require.NotNil(t, td.RuntimePlatform)
	assert.Equal(t, "ARM64", td.RuntimePlatform.CPUArchitecture)
	require.NotNil(t, td.ProxyConfiguration)
	assert.Equal(t, "envoy", td.ProxyConfiguration.ContainerName)
	assert.Equal(t, "IgnoredUID", td.ProxyConfiguration.Properties[0].Name)
	require.Len(t, td.PlacementConstraints, 1)
	assert.Equal(t, "memberOf", td.PlacementConstraints[0].Type)
	require.Len(t, td.InferenceAccelerators, 1)
	assert.Equal(t, "eia2.medium", td.InferenceAccelerators[0].DeviceType)
	assert.True(t, td.EnableFaultInjection)
}

func TestRegisterTaskDefinitionRejectsEmptyContainerName(t *testing.T) {
	m := newTestMock()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Image: "img"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, excClient, apiErr.ECSException())
}

func TestRegisterTaskDefinitionRejectsDuplicateContainerNames(t *testing.T) {
	m := newTestMock()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family: "web",
		ContainerDefinitions: []driver.ContainerDefinition{
			{Name: "app", Image: "img"},
			{Name: "app", Image: "img2"},
		},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, excClient, apiErr.ECSException())
}

// TestRegisterTaskDefinitionDeepCopyIsolation asserts a returned clone never
// aliases stored state: mutating the returned nested fields must not leak into a
// subsequent Describe.
func TestRegisterTaskDefinitionDeepCopyIsolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	reg, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{richContainerDef()},
		Volumes:              []driver.Volume{{Name: "data", Host: &driver.HostVolumeProperties{SourcePath: "/mnt"}}},
	})
	require.NoError(t, err)

	// Mutate every mutable nested field on the returned clone.
	reg.ContainerDefinitions[0].Environment[0].Value = "MUTATED"
	reg.ContainerDefinitions[0].LogConfiguration.Options["awslogs-group"] = "MUTATED"
	reg.ContainerDefinitions[0].HealthCheck.Retries = 999
	reg.ContainerDefinitions[0].LinuxParameters[0] = 'X'
	reg.Volumes[0].Host.SourcePath = "MUTATED"

	td, err := m.DescribeTaskDefinition(ctx, reg.ARN)
	require.NoError(t, err)

	c := td.ContainerDefinitions[0]
	assert.Equal(t, "prod", c.Environment[0].Value)
	assert.Equal(t, "/ecs/app", c.LogConfiguration.Options["awslogs-group"])
	assert.Equal(t, 3, c.HealthCheck.Retries)
	assert.JSONEq(t, `{"initProcessEnabled":true}`, string(c.LinuxParameters))
	assert.Equal(t, "/mnt", td.Volumes[0].Host.SourcePath)
}

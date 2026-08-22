// Package driver defines the interface for Azure Container Instances (ACI)
// implementations. ACI is an Azure-only service — it models
// Microsoft.ContainerInstance/containerGroups, a group of one or more
// containers scheduled together — so, unlike the portable multi-cloud services,
// it has a single provider implementation.
package driver

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// EnvVar is a single environment variable exposed to a container.
type EnvVar struct {
	Name  string
	Value string
}

// ContainerConfig describes one container to run within a container group.
type ContainerConfig struct {
	Name       string
	Image      string
	Command    []string
	CPU        float64
	MemoryInGB float64
	Env        []EnvVar
}

// ContainerGroupConfig describes a container group to create or update. It maps
// the request body of a Microsoft.ContainerInstance/containerGroups PUT onto
// the fields the provider records.
type ContainerGroupConfig struct {
	Name          string
	Location      string
	OSType        string // "Linux" or "Windows"
	RestartPolicy string // "Always", "OnFailure", or "Never"
	Containers    []ContainerConfig
	Tags          map[string]string
	Scope         scope.Scope
}

// ContainerState is the observed lifecycle state of a single container, mirroring
// ACI's instanceView.currentState. State is the ACI vocabulary ("Running",
// "Terminated", "Waiting"); Terminated carries an exit code and finish time.
type ContainerState struct {
	State        string
	ExitCode     int
	HasExitCode  bool // true once the container has terminated with an exit code
	StartTime    time.Time
	FinishTime   time.Time
	DetailStatus string
}

// ContainerInstance is a container within a group plus its observed state.
type ContainerInstance struct {
	Name       string
	Image      string
	Command    []string
	CPU        float64
	MemoryInGB float64
	Env        []EnvVar
	Current    ContainerState
}

// ContainerGroup is the recorded state of a container group.
type ContainerGroup struct {
	Name              string
	Location          string
	OSType            string
	RestartPolicy     string
	ProvisioningState string // "Succeeded"
	State             string // group-level instanceView.state ("Running", "Succeeded")
	Containers        []ContainerInstance
	Tags              map[string]string
	Scope             scope.Scope
}

// ContainerInstances is the interface an Azure Container Instances provider must
// satisfy. It is deliberately small: the container-group control plane plus the
// per-container logs read.
type ContainerInstances interface {
	// CreateContainerGroup creates the group, or replaces it when one of the
	// same name already exists (ARM PUT is create-or-update). When a
	// config.ContainerEngine is wired the group's containers run for real and
	// the returned state reflects the engine's observed states/exit codes.
	CreateContainerGroup(ctx context.Context, cfg ContainerGroupConfig) (*ContainerGroup, error)

	// GetContainerGroup returns the recorded group, or a NotFound error.
	GetContainerGroup(ctx context.Context, name string) (*ContainerGroup, error)

	// DeleteContainerGroup removes the group, tearing down any engine-backed
	// workload first. Returns NotFound when the group does not exist.
	DeleteContainerGroup(ctx context.Context, name string) error

	// ListContainerGroups returns the groups visible under filter.
	ListContainerGroups(ctx context.Context, filter scope.Scope) ([]ContainerGroup, error)

	// ContainerLogs returns the captured stdout/stderr for one container in the
	// group. A non-positive tail returns the full log. It is empty for a group
	// that is not engine-backed.
	ContainerLogs(ctx context.Context, group, container string, tail int) (string, error)
}

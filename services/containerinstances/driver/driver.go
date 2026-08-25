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

// Port is a single port exposed on a container group's public/private IP.
type Port struct {
	Port     int
	Protocol string // "TCP" or "UDP"
}

// IPAddress is a container group's requested/assigned IP configuration
// (Microsoft.ContainerInstance/containerGroups properties.ipAddress). Type is
// "Public" or "Private". For a Public group with a DNSNameLabel the provider
// assigns an IP and computes an FQDN.
type IPAddress struct {
	Type         string // "Public" or "Private"
	Ports        []Port
	DNSNameLabel string
	IP           string // server-assigned for a Public group
	FQDN         string // computed from DNSNameLabel for a Public group
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
	IPAddress     *IPAddress
	Tags          map[string]string
	Scope         scope.Scope
}

// ExecSession is the connection descriptor returned by ExecContainer, mirroring
// ACI's ContainerExecResponse: a websocket URI to attach to and a one-time
// password to authenticate the exec stream.
type ExecSession struct {
	WebSocketURI string
	Password     string
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
	State             string // group-level instanceView.state ("Running", "Succeeded", "Stopped")
	Containers        []ContainerInstance
	IPAddress         *IPAddress
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

	// GetContainerGroup returns the recorded group scoped to subscription and
	// resourceGroup, or a NotFound error. A container group's ARM identity is
	// {subscription, resourceGroup, name} — the same name in a different
	// resource group (or subscription) is a different resource.
	GetContainerGroup(ctx context.Context, subscription, resourceGroup, name string) (*ContainerGroup, error)

	// DeleteContainerGroup removes the group scoped to subscription and
	// resourceGroup, tearing down any engine-backed workload first. Returns
	// NotFound when the group does not exist.
	DeleteContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error

	// ListContainerGroups returns the groups visible under filter.
	ListContainerGroups(ctx context.Context, filter scope.Scope) ([]ContainerGroup, error)

	// StartContainerGroup starts all containers in a stopped group, allocating
	// compute again. Returns NotFound when the group does not exist.
	StartContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error

	// StopContainerGroup stops all containers in the group and deallocates
	// compute, tearing down any engine-backed workload. Returns NotFound when the
	// group does not exist.
	StopContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error

	// RestartContainerGroup restarts all containers in the group. Returns NotFound
	// when the group does not exist.
	RestartContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error

	// ExecContainer opens an exec session on one container in the group and
	// returns its websocket URI and password. When the group is engine-backed the
	// command is run for real on the engine. Returns NotFound when the group or
	// container does not exist.
	ExecContainer(ctx context.Context, subscription, resourceGroup, group, container string, command []string) (*ExecSession, error)

	// ContainerLogs returns the captured stdout/stderr for one container in the
	// group. A non-positive tail returns the full log. It is empty for a group
	// that is not engine-backed.
	ContainerLogs(ctx context.Context, subscription, resourceGroup, group, container string, tail int) (string, error)
}

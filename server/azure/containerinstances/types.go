package containerinstances

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	containerGroupResourceType = "Microsoft.ContainerInstance/containerGroups"
	defaultLocation            = "eastus"
	timeFormat                 = "2006-01-02T15:04:05Z"
)

// containerGroupJSON mirrors the armcontainerinstance ContainerGroup resource.
// The same shape is decoded on input (PUT body) and encoded on output.
type containerGroupJSON struct {
	ID         string                    `json:"id,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Type       string                    `json:"type,omitempty"`
	Location   string                    `json:"location,omitempty"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Properties *containerGroupProperties `json:"properties,omitempty"`
}

// containerGroupProperties mirrors ContainerGroupProperties.
type containerGroupProperties struct {
	OSType            string             `json:"osType,omitempty"`
	RestartPolicy     string             `json:"restartPolicy,omitempty"`
	ProvisioningState string             `json:"provisioningState,omitempty"`
	Containers        []containerJSON    `json:"containers,omitempty"`
	InstanceView      *groupInstanceView `json:"instanceView,omitempty"`
}

// groupInstanceView mirrors the group-level instanceView.
type groupInstanceView struct {
	State  string `json:"state,omitempty"`
	Events []any  `json:"events"`
}

// containerJSON mirrors a Container entry.
type containerJSON struct {
	Name       string              `json:"name,omitempty"`
	Properties *containerPropsJSON `json:"properties,omitempty"`
}

// containerPropsJSON mirrors ContainerProperties.
type containerPropsJSON struct {
	Image                string                 `json:"image,omitempty"`
	Command              []string               `json:"command,omitempty"`
	EnvironmentVariables []envVarJSON           `json:"environmentVariables,omitempty"`
	Resources            *resourceRequirements  `json:"resources,omitempty"`
	InstanceView         *containerInstanceView `json:"instanceView,omitempty"`
}

// envVarJSON mirrors an EnvironmentVariable.
type envVarJSON struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// resourceRequirements mirrors ResourceRequirements.
type resourceRequirements struct {
	Requests *resourceRequests `json:"requests,omitempty"`
}

// resourceRequests mirrors ResourceRequests.
type resourceRequests struct {
	CPU        float64 `json:"cpu,omitempty"`
	MemoryInGB float64 `json:"memoryInGB,omitempty"`
}

// containerInstanceView mirrors ContainerPropertiesInstanceView.
type containerInstanceView struct {
	CurrentState *containerStateJSON `json:"currentState,omitempty"`
	RestartCount int                 `json:"restartCount"`
}

// containerStateJSON mirrors ContainerState (instanceView.currentState).
type containerStateJSON struct {
	State        string `json:"state,omitempty"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	StartTime    string `json:"startTime,omitempty"`
	FinishTime   string `json:"finishTime,omitempty"`
	DetailStatus string `json:"detailStatus,omitempty"`
}

type containerGroupListResult struct {
	Value []containerGroupJSON `json:"value"`
}

// logsJSON mirrors the ListLogs response body.
type logsJSON struct {
	Content string `json:"content"`
}

// toConfig maps a decoded PUT body onto the driver's create config.
func toConfig(rp *azurearm.ResourcePath, body *containerGroupJSON) driver.ContainerGroupConfig {
	loc := body.Location
	if loc == "" {
		loc = defaultLocation
	}

	cfg := driver.ContainerGroupConfig{
		Name:     rp.ResourceName,
		Location: loc,
		Tags:     body.Tags,
		Scope:    scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}

	if body.Properties != nil {
		cfg.OSType = body.Properties.OSType
		cfg.RestartPolicy = body.Properties.RestartPolicy
		cfg.Containers = toContainerConfigs(body.Properties.Containers)
	}

	return cfg
}

// toContainerConfigs maps the request's container entries onto driver configs.
func toContainerConfigs(in []containerJSON) []driver.ContainerConfig {
	out := make([]driver.ContainerConfig, 0, len(in))

	for i := range in {
		c := &in[i]
		cc := driver.ContainerConfig{Name: c.Name}

		if c.Properties != nil {
			cc.Image = c.Properties.Image
			cc.Command = append([]string(nil), c.Properties.Command...)
			cc.Env = toEnvVars(c.Properties.EnvironmentVariables)

			if res := c.Properties.Resources; res != nil && res.Requests != nil {
				cc.CPU = res.Requests.CPU
				cc.MemoryInGB = res.Requests.MemoryInGB
			}
		}

		out = append(out, cc)
	}

	return out
}

// toEnvVars maps request env entries onto driver env vars.
func toEnvVars(in []envVarJSON) []driver.EnvVar {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.EnvVar, 0, len(in))
	for _, e := range in {
		out = append(out, driver.EnvVar{Name: e.Name, Value: e.Value})
	}

	return out
}

package aro

import "github.com/stackshy/cloudemu/v2/providers/azure/aro"

// ARM resource type identifiers for Microsoft.RedHatOpenShift.
const (
	resourceTypeOpenShiftClusters = "openShiftClusters"
	resourceTypeOpenShiftFull     = "Microsoft.RedHatOpenShift/openShiftClusters"
)

// armOpenShiftCluster mirrors the JSON shape ARM uses for
// Microsoft.RedHatOpenShift/openShiftClusters. Only the fields cloudemu wires
// through are modeled; unknown fields decode and drop harmlessly.
type armOpenShiftCluster struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Type       string             `json:"type,omitempty"`
	Location   string             `json:"location,omitempty"`
	Tags       map[string]*string `json:"tags,omitempty"`
	Properties *armOSProperties   `json:"properties,omitempty"`
}

type armOSProperties struct {
	ProvisioningState string               `json:"provisioningState,omitempty"`
	ClusterProfile    *armClusterProfile   `json:"clusterProfile,omitempty"`
	ConsoleProfile    *armConsoleProfile   `json:"consoleProfile,omitempty"`
	APIServerProfile  *armAPIServerProfile `json:"apiserverProfile,omitempty"`
}

type armClusterProfile struct {
	Version string `json:"version,omitempty"`
	Domain  string `json:"domain,omitempty"`
}

type armConsoleProfile struct {
	URL string `json:"url,omitempty"`
}

type armAPIServerProfile struct {
	URL        string `json:"url,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// armAdminKubeconfig is the listAdminCredentials response (base64-encoded
// kubeconfig, encoded automatically because the field is []byte).
type armAdminKubeconfig struct {
	Kubeconfig []byte `json:"kubeconfig,omitempty"`
}

// armCredentials is the listCredentials response (kubeadmin username/password).
type armCredentials struct {
	KubeadminUsername string `json:"kubeadminUsername,omitempty"`
	KubeadminPassword string `json:"kubeadminPassword,omitempty"`
}

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// toARMCluster converts a stored ARO cluster to its ARM JSON shape.
func toARMCluster(c *aro.OpenShiftCluster) armOpenShiftCluster {
	return armOpenShiftCluster{
		ID:       c.ID,
		Name:     c.Name,
		Type:     resourceTypeOpenShiftFull,
		Location: c.Location,
		Tags:     toPtrTags(c.Tags),
		Properties: &armOSProperties{
			ProvisioningState: c.ProvisioningState,
			ClusterProfile:    &armClusterProfile{Version: c.Version},
			ConsoleProfile:    &armConsoleProfile{URL: c.ConsoleURL},
			APIServerProfile:  &armAPIServerProfile{URL: c.APIServerURL, Visibility: "Public"},
		},
	}
}

func toPtrTags(in map[string]string) map[string]*string {
	if in == nil {
		return nil
	}

	out := make(map[string]*string, len(in))

	for k, v := range in {
		val := v
		out[k] = &val
	}

	return out
}

func fromPtrTags(in map[string]*string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if v != nil {
			out[k] = *v
		}
	}

	return out
}

package ocm

import "github.com/stackshy/cloudemu/v2/providers/openshift/ocm"

// OCM wire shapes for /api/clusters_mgmt/v1. OCM models every resource with a
// "kind" discriminator and link sub-objects ({kind:"...Link", id:"..."}); the
// rosa CLI and OCM SDK decode these shapes.

type ocmLink struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
	Href string `json:"href,omitempty"`
}

type ocmURL struct {
	URL string `json:"url,omitempty"`
}

type ocmCluster struct {
	Kind             string  `json:"kind"`
	ID               string  `json:"id"`
	Href             string  `json:"href"`
	Name             string  `json:"name"`
	State            string  `json:"state"`
	CloudProvider    ocmLink `json:"cloud_provider"`
	Region           ocmLink `json:"region"`
	OpenShiftVersion string  `json:"openshift_version"`
	Version          ocmLink `json:"version"`
	API              ocmURL  `json:"api"`
	Console          ocmURL  `json:"console"`
	Product          ocmLink `json:"product"`
}

type ocmClusterList struct {
	Kind  string       `json:"kind"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
	Total int          `json:"total"`
	Items []ocmCluster `json:"items"`
}

// ocmCredentials is the clusters/{id}/credentials response — OCM returns the
// kubeconfig as a raw YAML string.
type ocmCredentials struct {
	Kind       string `json:"kind"`
	Kubeconfig string `json:"kubeconfig"`
}

// ocmError is the OCM error envelope.
type ocmError struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// tokenResponse is the SSO token endpoint response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

const clustersHref = "/api/clusters_mgmt/v1/clusters/"

// toOCMCluster converts a stored cluster to its OCM JSON shape.
func toOCMCluster(c *ocm.Cluster) ocmCluster {
	return ocmCluster{
		Kind:             "Cluster",
		ID:               c.ID,
		Href:             clustersHref + c.ID,
		Name:             c.Name,
		State:            c.State,
		CloudProvider:    ocmLink{Kind: "CloudProviderLink", ID: c.CloudProvider},
		Region:           ocmLink{Kind: "CloudRegionLink", ID: c.Region},
		OpenShiftVersion: c.Version,
		Version:          ocmLink{Kind: "VersionLink", ID: "openshift-v" + c.Version},
		API:              ocmURL{URL: c.APIURL},
		Console:          ocmURL{URL: c.ConsoleURL},
		Product:          ocmLink{Kind: "ProductLink", ID: c.Product},
	}
}

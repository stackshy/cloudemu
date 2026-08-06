package functions

// siteResource is the ARM JSON shape for Microsoft.Web/sites returned to the
// SDK. We populate the fields a Functions client needs to navigate the site
// (id, name, type, kind, location, basic properties) and skip the long tail
// of feature flags real App Service surfaces.
type siteResource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Kind       string            `json:"kind"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties siteProperties    `json:"properties"`
}

type siteProperties struct {
	State               string            `json:"state"`
	HostNames           []string          `json:"hostNames"`
	DefaultHostName     string            `json:"defaultHostName"`
	SiteConfig          siteConfig        `json:"siteConfig"`
	Reserved            bool              `json:"reserved,omitempty"`
	ServerFarmID        string            `json:"serverFarmId,omitempty"`
	HTTPSOnly           bool              `json:"httpsOnly,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	LastModifiedTimeUtc string            `json:"lastModifiedTimeUtc,omitempty"`
}

type siteConfig struct {
	LinuxFxVersion      string      `json:"linuxFxVersion,omitempty"`
	NetFrameworkVersion string      `json:"netFrameworkVersion,omitempty"`
	AppSettings         []nameValue `json:"appSettings,omitempty"`
}

type nameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// siteListResponse is the {value: [...]} envelope ARM uses for collection responses.
type siteListResponse struct {
	Value []siteResource `json:"value"`
}

// createSiteRequest captures the fields we read from a PUT body. Real Azure
// accepts dozens of properties; the driver only models the basics.
type createSiteRequest struct {
	Kind       string               `json:"kind"`
	Location   string               `json:"location"`
	Tags       map[string]string    `json:"tags"`
	Properties createSiteProperties `json:"properties"`
}

type createSiteProperties struct {
	SiteConfig   createSiteConfig `json:"siteConfig"`
	Reserved     bool             `json:"reserved"`
	ServerFarmID string           `json:"serverFarmId"`
	HTTPSOnly    bool             `json:"httpsOnly"`
}

type createSiteConfig struct {
	LinuxFxVersion string      `json:"linuxFxVersion"`
	AppSettings    []nameValue `json:"appSettings"`
}

// serverFarmResource is the ARM JSON shape for Microsoft.Web/serverfarms (App
// Service plans) returned to the SDK. The SKU carries the pricing tier a plan
// bills on — the fields an armappservice PlansClient reads back.
type serverFarmResource struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Kind       string               `json:"kind,omitempty"`
	Location   string               `json:"location"`
	SKU        *serverFarmSKU       `json:"sku,omitempty"`
	Tags       map[string]string    `json:"tags,omitempty"`
	Properties serverFarmProperties `json:"properties"`
}

type serverFarmSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type serverFarmProperties struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
	Status            string `json:"status,omitempty"`
}

// createServerFarmRequest captures the fields read from a serverfarms PUT body.
type createServerFarmRequest struct {
	Kind     string            `json:"kind"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	SKU      serverFarmSKU     `json:"sku"`
}

package driver

import "context"

// Project status values.
const (
	ProjectPending         = "Pending"
	ProjectCreateInProgres = "CreateInProgress"
	ProjectCreateCompleted = "CreateCompleted"
	ProjectUpdateInProgres = "UpdateInProgress"
	ProjectUpdateCompleted = "UpdateCompleted"
	ProjectDeleteInProgres = "DeleteInProgress"
)

// MaxProjects is the per-account, per-region project quota.
const MaxProjects = 500

// ProvisioningParameter is a Service Catalog / CloudFormation parameter.
type ProvisioningParameter struct {
	Key   string
	Value string
}

// ServiceCatalogProvisioning identifies the Service Catalog product a project
// is provisioned from.
type ServiceCatalogProvisioning struct {
	ProductID              string
	PathID                 string
	ProvisioningArtifactID string
	Parameters             []ProvisioningParameter
}

// TemplateProvider is a CloudFormation template a project is provisioned from.
type TemplateProvider struct {
	TemplateName string
	TemplateURL  string
	RoleARN      string
	Parameters   []ProvisioningParameter
}

// ProjectSpec describes a project to create. Exactly one of ServiceCatalog and
// TemplateProviders is set.
type ProjectSpec struct {
	Name              string
	Description       string
	ServiceCatalog    *ServiceCatalogProvisioning
	TemplateProviders []TemplateProvider
	Tags              []Tag
}

// Project is an ML project.
type Project struct {
	Name                     string
	ARN                      string
	ID                       string
	Description              string
	Status                   string
	ServiceCatalog           *ServiceCatalogProvisioning
	ProvisionedProductID     string
	ProvisionedStatusMessage string
	TemplateProviders        []TemplateProvider
	CreationTime             string
	LastModifiedTime         string
	Tags                     []Tag
}

// ProjectUpdate describes an UpdateProject request; a nil field is unchanged.
type ProjectUpdate struct {
	Name                      string
	Description               *string
	ProvisioningArtifactID    string
	Parameters                []ProvisioningParameter
	Tags                      []Tag
	TemplateProvidersToUpdate []TemplateProvider
}

// projectAPI covers ML projects.
type projectAPI interface {
	CreateProject(ctx context.Context, spec ProjectSpec) (*Project, error)
	DescribeProject(ctx context.Context, name string) (*Project, error)
	ListProjects(ctx context.Context, opts ListOptions) (ListPage[Project], error)
	UpdateProject(ctx context.Context, update ProjectUpdate) (*Project, error)
	DeleteProject(ctx context.Context, name string) error
}

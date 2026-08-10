package driver

import "context"

// crawlerAPI covers crawlers and classifiers with real state.
type crawlerAPI interface {
	// Crawlers.
	CreateCrawler(ctx context.Context, c Crawler) error
	GetCrawler(ctx context.Context, name string) (*Crawler, error)
	UpdateCrawler(ctx context.Context, name string, c Crawler) error
	DeleteCrawler(ctx context.Context, name string) error
	GetCrawlers(ctx context.Context, page TablePagination) ([]Crawler, string, error)
	ListCrawlers(ctx context.Context, page TablePagination) ([]string, string, error)
	StartCrawler(ctx context.Context, name string) error
	StopCrawler(ctx context.Context, name string) error
	BatchGetCrawlers(ctx context.Context, names []string) ([]Crawler, []string, error)

	// Classifiers.
	CreateClassifier(ctx context.Context, c Classifier) error
	GetClassifier(ctx context.Context, name string) (*Classifier, error)
	UpdateClassifier(ctx context.Context, c Classifier) error
	DeleteClassifier(ctx context.Context, name string) error
	GetClassifiers(ctx context.Context, page TablePagination) ([]Classifier, string, error)
}

// jobAPI covers ETL jobs and their runs with real state. A started job run
// completes SUCCEEDED synchronously (documented) because there is no real Spark
// compute plane behind the emulator.
type jobAPI interface {
	CreateJob(ctx context.Context, j Job) (string, error)
	GetJob(ctx context.Context, name string) (*Job, error)
	UpdateJob(ctx context.Context, name string, j Job) (string, error)
	DeleteJob(ctx context.Context, name string) (string, error)
	GetJobs(ctx context.Context, page TablePagination) ([]Job, string, error)
	ListJobs(ctx context.Context, page TablePagination) ([]string, string, error)
	BatchGetJobs(ctx context.Context, names []string) ([]Job, []string, error)

	StartJobRun(ctx context.Context, jobName string, args map[string]string) (string, error)
	GetJobRun(ctx context.Context, jobName, runID string) (*JobRun, error)
	GetJobRuns(ctx context.Context, jobName string, page TablePagination) ([]JobRun, string, error)
	BatchStopJobRun(ctx context.Context, jobName string, runIDs []string) (successful []string, errors []BatchError)
}

// triggerAPI covers workflow triggers with real state.
type triggerAPI interface {
	CreateTrigger(ctx context.Context, t Trigger) (string, error)
	GetTrigger(ctx context.Context, name string) (*Trigger, error)
	UpdateTrigger(ctx context.Context, name string, t Trigger) (*Trigger, error)
	DeleteTrigger(ctx context.Context, name string) (string, error)
	GetTriggers(ctx context.Context, page TablePagination) ([]Trigger, string, error)
	ListTriggers(ctx context.Context, page TablePagination) ([]string, string, error)
	StartTrigger(ctx context.Context, name string) (string, error)
	StopTrigger(ctx context.Context, name string) (string, error)
	BatchGetTriggers(ctx context.Context, names []string) ([]Trigger, []string, error)
}

// workflowAPI covers workflows, their runs, and blueprints with real state.
type workflowAPI interface {
	CreateWorkflow(ctx context.Context, w Workflow) (string, error)
	GetWorkflow(ctx context.Context, name string) (*Workflow, error)
	UpdateWorkflow(ctx context.Context, name string, w Workflow) (string, error)
	DeleteWorkflow(ctx context.Context, name string) (string, error)
	ListWorkflows(ctx context.Context, page TablePagination) ([]string, string, error)
	BatchGetWorkflows(ctx context.Context, names []string) ([]Workflow, []string, error)
	StartWorkflowRun(ctx context.Context, name string) (string, error)
	GetWorkflowRun(ctx context.Context, name, runID string) (*WorkflowRun, error)
	GetWorkflowRuns(ctx context.Context, name string, page TablePagination) ([]WorkflowRun, string, error)
	StopWorkflowRun(ctx context.Context, name, runID string) error
	ResumeWorkflowRun(ctx context.Context, name, runID string, nodeIDs []string) (string, error)
	GetWorkflowRunProperties(ctx context.Context, name, runID string) (map[string]string, error)
	PutWorkflowRunProperties(ctx context.Context, name, runID string, props map[string]string) error

	CreateBlueprint(ctx context.Context, b Blueprint) (string, error)
	GetBlueprint(ctx context.Context, name string) (*Blueprint, error)
	UpdateBlueprint(ctx context.Context, name string, b Blueprint) (string, error)
	DeleteBlueprint(ctx context.Context, name string) (string, error)
	ListBlueprints(ctx context.Context, page TablePagination) ([]string, string, error)
	BatchGetBlueprints(ctx context.Context, names []string) ([]Blueprint, []string, error)
	StartBlueprintRun(ctx context.Context, name, roleARN, parameters string) (string, error)
	GetBlueprintRun(ctx context.Context, name, runID string) (*BlueprintRun, error)
	GetBlueprintRuns(ctx context.Context, name string, page TablePagination) ([]BlueprintRun, string, error)
}

// registryAPI covers the schema registry (registries, schemas, versions) and
// security configurations and dev endpoints with real state.
type registryAPI interface {
	CreateRegistry(ctx context.Context, r Registry) (*Registry, error)
	GetRegistry(ctx context.Context, name string) (*Registry, error)
	UpdateRegistry(ctx context.Context, name, description string) (*Registry, error)
	DeleteRegistry(ctx context.Context, name string) (*Registry, error)
	ListRegistries(ctx context.Context, page TablePagination) ([]Registry, string, error)

	CreateSchema(ctx context.Context, s Schema, initialDefinition string) (*Schema, error)
	GetSchema(ctx context.Context, registryName, schemaName string) (*Schema, error)
	UpdateSchema(ctx context.Context, registryName, schemaName, compatibility, description string) (*Schema, error)
	DeleteSchema(ctx context.Context, registryName, schemaName string) (*Schema, error)
	ListSchemas(ctx context.Context, registryName string, page TablePagination) ([]Schema, string, error)

	RegisterSchemaVersion(ctx context.Context, registryName, schemaName, definition string) (*SchemaVersion, error)
	GetSchemaVersion(ctx context.Context, registryName, schemaName, versionID string, versionNumber int64) (*SchemaVersion, error)
	GetSchemaByDefinition(ctx context.Context, registryName, schemaName, definition string) (*SchemaVersion, error)
	ListSchemaVersions(ctx context.Context, registryName, schemaName string, page TablePagination) ([]SchemaVersion, string, error)
	DeleteSchemaVersions(ctx context.Context, registryName, schemaName, versions string) ([]BatchError, error)
	CheckSchemaVersionValidity(ctx context.Context, dataFormat, definition string) (bool, string)
	GetSchemaVersionsDiff(ctx context.Context, registryName, schemaName, first, second string) (string, error)

	CreateSecurityConfiguration(ctx context.Context, sc SecurityConfiguration) error
	GetSecurityConfiguration(ctx context.Context, name string) (*SecurityConfiguration, error)
	DeleteSecurityConfiguration(ctx context.Context, name string) error
	GetSecurityConfigurations(ctx context.Context, page TablePagination) ([]SecurityConfiguration, string, error)

	CreateDevEndpoint(ctx context.Context, e DevEndpoint) (*DevEndpoint, error)
	GetDevEndpoint(ctx context.Context, name string) (*DevEndpoint, error)
	UpdateDevEndpoint(ctx context.Context, name string, args map[string]string) error
	DeleteDevEndpoint(ctx context.Context, name string) error
	GetDevEndpoints(ctx context.Context, page TablePagination) ([]DevEndpoint, string, error)
	ListDevEndpoints(ctx context.Context, page TablePagination) ([]string, string, error)
	BatchGetDevEndpoints(ctx context.Context, names []string) ([]DevEndpoint, []string, error)
}

// miscAPI covers tags and small stateful catalog-config operations.
type miscAPI interface {
	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, keys []string) error
	GetTags(ctx context.Context, resourceARN string) (map[string]string, error)

	PutResourcePolicy(ctx context.Context, arn, policy string, cond PolicyCondition) (string, error)
	GetResourcePolicy(ctx context.Context, arn string) (string, error)
	DeleteResourcePolicy(ctx context.Context, arn string) error

	PutDataCatalogEncryptionSettings(ctx context.Context, catalogID string, settings map[string]any) error
	GetDataCatalogEncryptionSettings(ctx context.Context, catalogID string) (map[string]any, error)
}

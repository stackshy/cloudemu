// Package bedrock provides an in-memory mock implementation of AWS Bedrock:
// a foundation-model catalog, a synchronous model-customization lifecycle,
// and an emulated inference runtime (InvokeModel, Converse).
package bedrock

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// Compile-time check that Mock implements driver.Bedrock.
var _ driver.Bedrock = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS Bedrock service.
type Mock struct {
	foundation  []driver.FoundationModel
	sysProfiles []driver.InferenceProfile // SYSTEM_DEFINED profiles, immutable like the catalog
	jobs        *memstore.Store[*driver.CustomizationJob]
	models      *memstore.Store[*driver.CustomModel]
	guardrails  *memstore.Store[*guardrailRecord]
	provisioned *memstore.Store[*driver.ProvisionedThroughput]
	tags        *memstore.Store[[]driver.Tag] // keyed by resource ARN

	asyncInvokes *memstore.Store[*driver.AsyncInvoke]    // keyed by invocation ARN
	importJobs   *memstore.Store[*driver.ModelImportJob] // keyed by job name
	copyJobs     *memstore.Store[*driver.ModelCopyJob]   // keyed by job ARN
	evalJobs     *memstore.Store[*driver.EvaluationJob]  // keyed by job name

	inferenceProfiles *memstore.Store[*driver.InferenceProfile]         // keyed by profile ID
	promptRouters     *memstore.Store[*driver.PromptRouter]             // keyed by router ARN
	arPolicies        *memstore.Store[*driver.AutomatedReasoningPolicy] // keyed by policy ARN

	marketplaceEndpoints *memstore.Store[*driver.MarketplaceEndpoint] // keyed by endpoint ARN
	fmAgreements         *memstore.Store[bool]                        // set of accepted agreements keyed by modelId

	opts *config.Options

	logMu   sync.RWMutex
	logging *driver.LoggingConfig
}

// New creates a new Bedrock mock seeded with a realistic foundation-model
// catalog.
func New(opts *config.Options) *Mock {
	catalog := seedFoundationModels(opts.Region)
	seededAt := opts.Clock.Now().UTC().Format(time.RFC3339)

	return &Mock{
		foundation:  catalog,
		sysProfiles: seedSystemProfiles(opts.Region, opts.AccountID, seededAt, catalog),
		jobs:        memstore.New[*driver.CustomizationJob](),
		models:      memstore.New[*driver.CustomModel](),
		guardrails:  memstore.New[*guardrailRecord](),
		provisioned: memstore.New[*driver.ProvisionedThroughput](),
		tags:        memstore.New[[]driver.Tag](),

		asyncInvokes: memstore.New[*driver.AsyncInvoke](),
		importJobs:   memstore.New[*driver.ModelImportJob](),
		copyJobs:     memstore.New[*driver.ModelCopyJob](),
		evalJobs:     memstore.New[*driver.EvaluationJob](),

		inferenceProfiles: memstore.New[*driver.InferenceProfile](),
		promptRouters:     memstore.New[*driver.PromptRouter](),
		arPolicies:        memstore.New[*driver.AutomatedReasoningPolicy](),

		marketplaceEndpoints: memstore.New[*driver.MarketplaceEndpoint](),
		fmAgreements:         memstore.New[bool](),

		opts: opts,
	}
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// ListFoundationModels returns the catalog models that pass filter.
func (m *Mock) ListFoundationModels(_ context.Context, filter driver.FoundationModelFilter) ([]driver.FoundationModel, error) {
	if err := validateFoundationFilter(filter); err != nil {
		return nil, err
	}

	out := make([]driver.FoundationModel, 0, len(m.foundation))

	for i := range m.foundation {
		if matchesFoundationFilter(&m.foundation[i], filter) {
			out = append(out, cloneFoundationModel(m.foundation[i]))
		}
	}

	return out, nil
}

// GetFoundationModel returns one foundation model by ID or ARN.
func (m *Mock) GetFoundationModel(_ context.Context, modelID string) (*driver.FoundationModel, error) {
	fm := m.findFoundation(modelID)
	if fm == nil {
		return nil, errors.Newf(errors.NotFound, "foundation model %q not found", modelID)
	}

	result := cloneFoundationModel(*fm)

	return &result, nil
}

// cloneFoundationModel returns a copy of fm with its slice fields deep-copied so
// callers cannot mutate the shared seed catalog through the returned value.
//
//nolint:gocritic // fm is copied intentionally so slice fields can be reassigned to fresh backing arrays.
func cloneFoundationModel(fm driver.FoundationModel) driver.FoundationModel {
	fm.InputModalities = append([]string(nil), fm.InputModalities...)
	fm.OutputModalities = append([]string(nil), fm.OutputModalities...)
	fm.CustomizationsSupported = append([]string(nil), fm.CustomizationsSupported...)
	fm.InferenceTypesSupported = append([]string(nil), fm.InferenceTypesSupported...)

	return fm
}

// findFoundation returns the seeded model matching id by ModelID or ModelARN.
func (m *Mock) findFoundation(id string) *driver.FoundationModel {
	for i := range m.foundation {
		if m.foundation[i].ModelID == id || m.foundation[i].ModelARN == id {
			return &m.foundation[i]
		}
	}

	return nil
}

// CreateModelCustomizationJob starts a fine-tuning job. The job completes
// synchronously: it transitions straight to Completed and materializes an
// active custom model, so Get/List calls are deterministic.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateModelCustomizationJob(_ context.Context, cfg driver.CustomizationJobConfig) (*driver.CustomizationJob, error) {
	if err := validateJobConfig(cfg); err != nil {
		return nil, err
	}

	base := m.findFoundation(cfg.BaseModelIdentifier)
	if base == nil {
		return nil, errors.Newf(errors.InvalidArgument, "base model %q not found", cfg.BaseModelIdentifier)
	}

	if m.jobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "customization job %q already exists", cfg.JobName)
	}

	if m.models.Has(cfg.CustomModelName) {
		return nil, errors.Newf(errors.AlreadyExists, "custom model %q already exists", cfg.CustomModelName)
	}

	now := m.now()
	jobARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "model-customization-job/"+idgen.GenerateID(""))
	modelARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "custom-model/"+cfg.CustomModelName)

	job := &driver.CustomizationJob{
		JobARN:             jobARN,
		JobName:            cfg.JobName,
		OutputModelName:    cfg.CustomModelName,
		OutputModelARN:     modelARN,
		RoleARN:            cfg.RoleARN,
		BaseModelARN:       base.ModelARN,
		Status:             driver.JobCompleted,
		CustomizationType:  defaultCustomizationType(cfg.CustomizationType),
		HyperParameters:    copyMap(cfg.HyperParameters),
		ClientRequestToken: cfg.ClientRequestToken,
		TrainingDataURI:    cfg.TrainingDataURI,
		OutputDataURI:      cfg.OutputDataURI,
		CreationTime:       now,
		LastModifiedTime:   now,
		EndTime:            now,
	}
	m.jobs.Set(cfg.JobName, job)

	m.models.Set(cfg.CustomModelName, &driver.CustomModel{
		ModelARN:          modelARN,
		ModelName:         cfg.CustomModelName,
		BaseModelARN:      base.ModelARN,
		BaseModelName:     base.ModelName,
		CustomizationType: job.CustomizationType,
		ModelStatus:       driver.ModelActive,
		JobARN:            jobARN,
		JobName:           cfg.JobName,
		HyperParameters:   copyMap(cfg.HyperParameters),
		TrainingDataURI:   cfg.TrainingDataURI,
		OutputDataURI:     cfg.OutputDataURI,
		OwnerAccountID:    m.opts.AccountID,
		CreationTime:      now,
	})

	result := *job

	return &result, nil
}

// GetModelCustomizationJob returns a job by name or ARN.
func (m *Mock) GetModelCustomizationJob(_ context.Context, jobIdentifier string) (*driver.CustomizationJob, error) {
	if job, ok := m.jobs.Get(jobIdentifier); ok {
		result := *job
		result.HyperParameters = copyMap(job.HyperParameters)

		return &result, nil
	}

	for _, job := range m.jobs.All() {
		if job.JobARN == jobIdentifier {
			result := *job
			result.HyperParameters = copyMap(job.HyperParameters)

			return &result, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "customization job %q not found", jobIdentifier)
}

// ListModelCustomizationJobs lists all customization jobs.
func (m *Mock) ListModelCustomizationJobs(_ context.Context) ([]driver.CustomizationJob, error) {
	all := m.jobs.SortedValues()
	out := make([]driver.CustomizationJob, 0, len(all))

	for _, job := range all {
		result := *job
		result.HyperParameters = copyMap(job.HyperParameters)
		out = append(out, result)
	}

	return out, nil
}

// ListCustomModels lists all custom models.
func (m *Mock) ListCustomModels(_ context.Context) ([]driver.CustomModel, error) {
	all := m.models.SortedValues()
	out := make([]driver.CustomModel, 0, len(all))

	for _, cm := range all {
		result := *cm
		result.HyperParameters = copyMap(cm.HyperParameters)
		out = append(out, result)
	}

	return out, nil
}

// GetCustomModel returns a custom model by name or ARN.
func (m *Mock) GetCustomModel(_ context.Context, modelIdentifier string) (*driver.CustomModel, error) {
	cm := m.findCustom(modelIdentifier)
	if cm == nil {
		return nil, errors.Newf(errors.NotFound, "custom model %q not found", modelIdentifier)
	}

	result := *cm
	result.HyperParameters = copyMap(cm.HyperParameters)

	return &result, nil
}

// DeleteCustomModel deletes a custom model by name or ARN.
func (m *Mock) DeleteCustomModel(_ context.Context, modelIdentifier string) error {
	cm := m.findCustom(modelIdentifier)
	if cm == nil {
		return errors.Newf(errors.NotFound, "custom model %q not found", modelIdentifier)
	}

	m.models.Delete(cm.ModelName)

	return nil
}

// findCustom returns the custom model matching id by name or ARN.
func (m *Mock) findCustom(id string) *driver.CustomModel {
	if cm, ok := m.models.Get(id); ok {
		return cm
	}

	for _, cm := range m.models.All() {
		if cm.ModelARN == id {
			return cm
		}
	}

	return nil
}

//nolint:gocritic // cfg matches the driver interface signature; validated without mutation.
func validateJobConfig(cfg driver.CustomizationJobConfig) error {
	switch {
	case cfg.JobName == "":
		return errors.New(errors.InvalidArgument, "jobName is required")
	case cfg.CustomModelName == "":
		return errors.New(errors.InvalidArgument, "customModelName is required")
	case cfg.RoleARN == "":
		return errors.New(errors.InvalidArgument, "roleArn is required")
	case cfg.BaseModelIdentifier == "":
		return errors.New(errors.InvalidArgument, "baseModelIdentifier is required")
	default:
		return nil
	}
}

func defaultCustomizationType(t string) string {
	if t == "" {
		return "FINE_TUNING"
	}

	return t
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

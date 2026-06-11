// Package bedrock provides an in-memory mock implementation of AWS Bedrock:
// a foundation-model catalog, a synchronous model-customization lifecycle,
// and an emulated inference runtime (InvokeModel, Converse).
package bedrock

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/bedrock/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
)

// Compile-time check that Mock implements driver.Bedrock.
var _ driver.Bedrock = (*Mock)(nil)

// Mock is an in-memory mock implementation of the AWS Bedrock service.
type Mock struct {
	foundation  []driver.FoundationModel
	jobs        *memstore.Store[*driver.CustomizationJob]
	models      *memstore.Store[*driver.CustomModel]
	guardrails  *memstore.Store[*driver.Guardrail]
	provisioned *memstore.Store[*driver.ProvisionedThroughput]
	opts        *config.Options

	logMu   sync.RWMutex
	logging *driver.LoggingConfig
}

// New creates a new Bedrock mock seeded with a realistic foundation-model
// catalog.
func New(opts *config.Options) *Mock {
	return &Mock{
		foundation:  seedFoundationModels(opts.Region),
		jobs:        memstore.New[*driver.CustomizationJob](),
		models:      memstore.New[*driver.CustomModel](),
		guardrails:  memstore.New[*driver.Guardrail](),
		provisioned: memstore.New[*driver.ProvisionedThroughput](),
		opts:        opts,
	}
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// ListFoundationModels returns the seeded foundation-model catalog.
func (m *Mock) ListFoundationModels(_ context.Context) ([]driver.FoundationModel, error) {
	out := make([]driver.FoundationModel, len(m.foundation))
	copy(out, m.foundation)

	return out, nil
}

// GetFoundationModel returns one foundation model by ID or ARN.
func (m *Mock) GetFoundationModel(_ context.Context, modelID string) (*driver.FoundationModel, error) {
	fm := m.findFoundation(modelID)
	if fm == nil {
		return nil, errors.Newf(errors.NotFound, "foundation model %q not found", modelID)
	}

	result := *fm

	return &result, nil
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

		return &result, nil
	}

	for _, job := range m.jobs.All() {
		if job.JobARN == jobIdentifier {
			result := *job

			return &result, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "customization job %q not found", jobIdentifier)
}

// ListModelCustomizationJobs lists all customization jobs.
func (m *Mock) ListModelCustomizationJobs(_ context.Context) ([]driver.CustomizationJob, error) {
	all := m.jobs.All()
	out := make([]driver.CustomizationJob, 0, len(all))

	for _, job := range all {
		out = append(out, *job)
	}

	return out, nil
}

// ListCustomModels lists all custom models.
func (m *Mock) ListCustomModels(_ context.Context) ([]driver.CustomModel, error) {
	all := m.models.All()
	out := make([]driver.CustomModel, 0, len(all))

	for _, cm := range all {
		out = append(out, *cm)
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

// fmARN builds a foundation-model ARN (no account component, per AWS).
func fmARN(region, modelID string) string {
	return idgen.AWSARN("bedrock", region, "", "foundation-model/"+modelID)
}

// seedFoundationModels returns a realistic catalog of foundation models.
func seedFoundationModels(region string) []driver.FoundationModel {
	text := []string{"TEXT"}
	embed := []string{"EMBEDDING"}
	onDemand := []string{"ON_DEMAND"}
	fineTune := []string{"FINE_TUNING"}

	specs := []driver.FoundationModel{
		{ModelID: "anthropic.claude-3-sonnet-20240229-v1:0", ModelName: "Claude 3 Sonnet", ProviderName: "Anthropic",
			InputModalities: text, OutputModalities: text, ResponseStreamingSupported: true, InferenceTypesSupported: onDemand},
		{ModelID: "anthropic.claude-3-haiku-20240307-v1:0", ModelName: "Claude 3 Haiku", ProviderName: "Anthropic",
			InputModalities: text, OutputModalities: text, ResponseStreamingSupported: true, InferenceTypesSupported: onDemand},
		{ModelID: "amazon.titan-text-express-v1", ModelName: "Titan Text G1 - Express", ProviderName: "Amazon",
			InputModalities: text, OutputModalities: text, ResponseStreamingSupported: true,
			CustomizationsSupported: fineTune, InferenceTypesSupported: onDemand},
		{ModelID: "amazon.titan-embed-text-v1", ModelName: "Titan Embeddings G1 - Text", ProviderName: "Amazon",
			InputModalities: text, OutputModalities: embed, InferenceTypesSupported: onDemand},
		{ModelID: "meta.llama3-8b-instruct-v1:0", ModelName: "Llama 3 8B Instruct", ProviderName: "Meta",
			InputModalities: text, OutputModalities: text, ResponseStreamingSupported: true,
			CustomizationsSupported: fineTune, InferenceTypesSupported: onDemand},
		{ModelID: "cohere.command-text-v14", ModelName: "Command", ProviderName: "Cohere",
			InputModalities: text, OutputModalities: text, ResponseStreamingSupported: true,
			CustomizationsSupported: fineTune, InferenceTypesSupported: onDemand},
	}

	for i := range specs {
		specs[i].ModelARN = fmARN(region, specs[i].ModelID)
		specs[i].LifecycleStatus = driver.LifecycleActive
	}

	return specs
}

// modelExists reports whether id names a known foundation or custom model.
func (m *Mock) modelExists(id string) bool {
	if m.findFoundation(id) != nil {
		return true
	}

	return m.findCustom(id) != nil
}

// familyOf classifies a model ID by provider prefix for response shaping.
func familyOf(modelID string) string {
	switch {
	case strings.HasPrefix(modelID, "anthropic."):
		return familyAnthropic
	case strings.HasPrefix(modelID, "amazon.titan"):
		return familyTitan
	case strings.HasPrefix(modelID, "meta.llama"):
		return familyLlama
	case strings.HasPrefix(modelID, "cohere."):
		return familyCohere
	default:
		return familyGeneric
	}
}

// Package sagemaker provides an in-memory mock implementation of Amazon
// SageMaker AI: training/processing/transform/tuning/AutoML/labeling/
// compilation jobs, the model-and-endpoint inference stack, the model
// registry, Studio, notebook instances, HyperPod clusters, Feature Store and
// pipelines. Asynchronous jobs complete synchronously (straight to a terminal
// state) so Describe/List calls are deterministic, mirroring the Bedrock mock.
package sagemaker

import (
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// Compile-time checks that Mock implements the driver interfaces.
var (
	_ driver.SageMaker = (*Mock)(nil)
	_ driver.Runtime   = (*Mock)(nil)
)

// Mock is an in-memory mock implementation of Amazon SageMaker AI.
type Mock struct {
	opts *config.Options

	trainingJobs    *memstore.Store[*driver.TrainingJob]
	processingJobs  *memstore.Store[*driver.ProcessingJob]
	transformJobs   *memstore.Store[*driver.TransformJob]
	tuningJobs      *memstore.Store[*driver.HyperParameterTuningJob]
	autoMLJobs      *memstore.Store[*driver.AutoMLJob]
	labelingJobs    *memstore.Store[*driver.LabelingJob]
	compilationJobs *memstore.Store[*driver.CompilationJob]

	models              *memstore.Store[*driver.Model]
	endpointConfigs     *memstore.Store[*driver.EndpointConfig]
	endpoints           *memstore.Store[*driver.Endpoint]
	inferenceComponents *memstore.Store[*driver.InferenceComponent]

	packageGroups *memstore.Store[*driver.ModelPackageGroup]
	packages      *memstore.Store[*driver.ModelPackage] // keyed by package ARN

	domains      *memstore.Store[*driver.Domain]
	userProfiles *memstore.Store[*driver.UserProfile] // keyed by domainID\x00name
	spaces       *memstore.Store[*driver.Space]       // keyed by domainID\x00name
	apps         *memstore.Store[*driver.App]         // keyed by app composite key

	notebooks   *memstore.Store[*driver.NotebookInstance]
	notebookLCs *memstore.Store[*driver.NotebookLifecycleConfig]
	codeRepos   *memstore.Store[*driver.CodeRepository]

	clusters *memstore.Store[*driver.Cluster]

	featureGroups  *memstore.Store[*driver.FeatureGroup]
	featureRecords *memstore.Store[[]driver.FeatureValue] // keyed by group\x00recordID

	pipelines   *memstore.Store[*driver.Pipeline]
	executions  *memstore.Store[*driver.PipelineExecution] // keyed by execution ARN
	experiments *memstore.Store[*driver.Experiment]
	trials      *memstore.Store[*driver.Trial]

	tags *memstore.Store[[]driver.Tag] // keyed by resource ARN

	monitoring mondriver.Monitoring
}

// New creates a new SageMaker mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		opts:                opts,
		trainingJobs:        memstore.New[*driver.TrainingJob](),
		processingJobs:      memstore.New[*driver.ProcessingJob](),
		transformJobs:       memstore.New[*driver.TransformJob](),
		tuningJobs:          memstore.New[*driver.HyperParameterTuningJob](),
		autoMLJobs:          memstore.New[*driver.AutoMLJob](),
		labelingJobs:        memstore.New[*driver.LabelingJob](),
		compilationJobs:     memstore.New[*driver.CompilationJob](),
		models:              memstore.New[*driver.Model](),
		endpointConfigs:     memstore.New[*driver.EndpointConfig](),
		endpoints:           memstore.New[*driver.Endpoint](),
		inferenceComponents: memstore.New[*driver.InferenceComponent](),
		packageGroups:       memstore.New[*driver.ModelPackageGroup](),
		packages:            memstore.New[*driver.ModelPackage](),
		domains:             memstore.New[*driver.Domain](),
		userProfiles:        memstore.New[*driver.UserProfile](),
		spaces:              memstore.New[*driver.Space](),
		apps:                memstore.New[*driver.App](),
		notebooks:           memstore.New[*driver.NotebookInstance](),
		notebookLCs:         memstore.New[*driver.NotebookLifecycleConfig](),
		codeRepos:           memstore.New[*driver.CodeRepository](),
		clusters:            memstore.New[*driver.Cluster](),
		featureGroups:       memstore.New[*driver.FeatureGroup](),
		featureRecords:      memstore.New[[]driver.FeatureValue](),
		pipelines:           memstore.New[*driver.Pipeline](),
		executions:          memstore.New[*driver.PipelineExecution](),
		experiments:         memstore.New[*driver.Experiment](),
		trials:              memstore.New[*driver.Trial](),
		tags:                memstore.New[[]driver.Tag](),
	}
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// arn builds a SageMaker ARN for the given resource segment (e.g.
// "training-job/my-job").
func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("sagemaker", m.opts.Region, m.opts.AccountID, resource)
}

func copyTags(in []driver.Tag) []driver.Tag {
	if in == nil {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}

func copyStrMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

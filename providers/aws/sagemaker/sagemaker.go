// Package sagemaker provides an in-memory mock implementation of Amazon
// SageMaker AI: training/processing/transform/tuning/AutoML/labeling/
// compilation jobs, the model-and-endpoint inference stack, the model
// registry, Studio, notebook instances, HyperPod clusters, Feature Store,
// pipelines, experiments and lineage, Model Monitor, projects, workforces and
// images. Jobs complete synchronously (straight to a terminal state with
// per-stage times) unless --async-settle is on, when they progress through
// their stages as the clock advances, so Describe/List calls stay deterministic.
//
// This is the Layer-3 driver implementation. The portable Layer-1 service that
// adds recording/metrics/rate-limiting/error-injection/latency lives in
// services/sagemaker.
package sagemaker

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
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
	// packagePolicies holds each model package group's resource policy, keyed by
	// group name.
	packagePolicies *memstore.Store[string]

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

	// monMu serializes Model Monitor state changes; monExecs holds materialized
	// (history-preserving) executions, monSettle the --async-settle windows.
	monMu        sync.Mutex
	monSchedules *memstore.Store[*driver.MonitoringSchedule]
	monDefs      *memstore.Store[*driver.MonitoringJobDefinition] // keyed by kind\x00name
	monExecs     *memstore.Store[*driver.MonitoringExecution]
	monAlerts    *memstore.Store[*driver.MonitoringAlert]
	monSettle    *settle.Set

	// entityMu serializes experiments, trials, trial components and lineage
	// entities/associations (one lock for the whole experiment-tracking graph,
	// so link updates and delete guards never deadlock).
	entityMu        sync.Mutex
	trialComponents *memstore.Store[*driver.TrialComponent] // keyed by lower-cased name
	lineageEntities *memstore.Store[*driver.LineageEntity]  // keyed by ARN
	associations    *memstore.Store[*driver.Association]    // keyed by source\x00destination
	lineageGroup    *memstore.Store[string]                 // "default" -> creation time

	// imageMu serializes image, image-version and alias changes (version numbers
	// are minted under it).
	imageMu         sync.Mutex
	images          *memstore.Store[*driver.Image]
	imageVersions   *memstore.Store[*driver.ImageVersion] // keyed by image\x00version
	appImageConfigs *memstore.Store[*driver.AppImageConfig]

	// wfMu guards the workforce singleton, the workteam cap and the workforce
	// <-> workteam links; wfSettle holds --async-settle windows.
	wfMu       sync.Mutex
	workforces *memstore.Store[*driver.Workforce]
	workteams  *memstore.Store[*driver.Workteam]
	wfSettle   *settle.Set

	// projMu guards the project cap and serializes project state changes;
	// projSettle holds --async-settle windows.
	projMu     sync.Mutex
	projects   *memstore.Store[*driver.Project]
	projSettle *settle.Set

	// epMu serializes every endpoint mutation and guards epTrans, the in-flight
	// (--async-settle) transitions overlaid on stored endpoints at read time.
	epMu sync.Mutex

	// invMu guards invFailures, the seeded per-endpoint model failures.
	invMu       sync.Mutex
	pipeMu      sync.Mutex // serializes pipeline update/delete against execution start/stop
	invFailures map[string]int
	epTrans     map[string]*endpointTransition

	// nbMu serializes notebook-instance state changes; nbSettle holds the
	// --async-settle Updating windows overlaid at read time.
	nbMu     sync.Mutex
	nbSettle *settle.Set

	// clMu serializes cluster mutations and guards clTrans, the in-flight
	// (--async-settle) windows overlaid on stored clusters at read time.
	clMu    sync.Mutex
	clTrans map[string]*clusterTransition

	// tagMu serializes every read-modify-write of the ARN-keyed tag store.
	tagMu sync.Mutex

	// pkgMu serializes the read-compute-write of model-package version numbers
	// so two concurrent CreateModelPackage calls can't mint the same ARN.
	pkgMu sync.Mutex

	events     awsevents.Emitter
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
		packagePolicies:     memstore.New[string](),
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
		projects:            memstore.New[*driver.Project](),
		workforces:          memstore.New[*driver.Workforce](),
		monSchedules:        memstore.New[*driver.MonitoringSchedule](),
		monDefs:             memstore.New[*driver.MonitoringJobDefinition](),
		monExecs:            memstore.New[*driver.MonitoringExecution](),
		monAlerts:           memstore.New[*driver.MonitoringAlert](),
		monSettle:           settle.NewSet(),
		invFailures:         map[string]int{},
		trialComponents:     memstore.New[*driver.TrialComponent](),
		lineageEntities:     memstore.New[*driver.LineageEntity](),
		associations:        memstore.New[*driver.Association](),
		lineageGroup:        memstore.New[string](),
		images:              memstore.New[*driver.Image](),
		imageVersions:       memstore.New[*driver.ImageVersion](),
		appImageConfigs:     memstore.New[*driver.AppImageConfig](),
		workteams:           memstore.New[*driver.Workteam](),
		wfSettle:            settle.NewSet(),
		projSettle:          settle.NewSet(),
		epTrans:             map[string]*endpointTransition{},
		nbSettle:            settle.NewSet(),
		clTrans:             map[string]*clusterTransition{},
	}
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// now returns the Clock time as RFC3339Nano so times within one operation can
// be ordered at sub-second precision (stage timelines, transition histories).
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339Nano)
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

// copyVariants returns a deep copy of a production-variant slice so stored
// endpoints/configs never share a backing array (mutating one would otherwise
// corrupt the source EndpointConfig and sibling endpoints).
func copyVariants(in []driver.ProductionVariant) []driver.ProductionVariant {
	if in == nil {
		return nil
	}

	out := make([]driver.ProductionVariant, len(in))
	copy(out, in)

	return out
}

// copyContainers returns a deep copy of a container-definition slice,
// including each container's Environment map. A plain slice copy would leave
// the maps aliased, so mutating a returned container's Environment would still
// corrupt the stored model.
func copyContainers(in []driver.ContainerDefinition) []driver.ContainerDefinition {
	if in == nil {
		return nil
	}

	out := make([]driver.ContainerDefinition, len(in))

	for i, c := range in {
		c.Environment = copyStrMap(c.Environment)
		out[i] = c
	}

	return out
}

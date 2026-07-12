// Package vertexai provides an in-memory mock implementation of Google Cloud
// Vertex AI (aiplatform.googleapis.com): datasets, the model registry,
// endpoints and online prediction, custom/training/tuning/batch jobs,
// pipelines, the Gemini generateContent runtime, Feature Store, Vector Search,
// Tensorboard, ML metadata, schedules and notebook runtimes.
//
// Control-plane mutations return an already-done Operation so SDK pollers
// terminate on the first poll. Job-family resources are created synchronously
// and driven straight to a terminal success state, mirroring how the real API
// returns the resource from create and exposes progress via a state field.
package vertexai

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// Compile-time check that Mock implements the full Vertex AI surface.
var _ driver.VertexAI = (*Mock)(nil)

// defaultLocation is used when a resource name carries no parseable location.
const defaultLocation = "us-central1"

// Mock is an in-memory mock implementation of Vertex AI.
type Mock struct {
	opts *config.Options

	datasets    *memstore.Store[*driver.Dataset]
	models      *memstore.Store[*driver.Model]
	evaluations *memstore.Store[*driver.ModelEvaluation]
	endpoints   *memstore.Store[*driver.Endpoint]

	customJobs    *memstore.Store[*driver.CustomJob]
	batchJobs     *memstore.Store[*driver.BatchPredictionJob]
	hpoJobs       *memstore.Store[*driver.HyperparameterTuningJob]
	trainPipes    *memstore.Store[*driver.TrainingPipeline]
	pipelineJobs  *memstore.Store[*driver.PipelineJob]
	tuningJobs    *memstore.Store[*driver.TuningJob]
	cachedContent *memstore.Store[*driver.CachedContent]

	featurestores *memstore.Store[*driver.Featurestore]
	entityTypes   *memstore.Store[*driver.EntityType]
	entityRecords *memstore.Store[[]driver.FeatureNameValue] // keyed by entityType\x00entityID
	featureGroups *memstore.Store[*driver.FeatureGroup]
	features      *memstore.Store[*driver.Feature]
	onlineStores  *memstore.Store[*driver.FeatureOnlineStore]
	featureViews  *memstore.Store[*driver.FeatureView]

	indexes        *memstore.Store[*driver.Index]
	indexEndpoints *memstore.Store[*driver.IndexEndpoint]

	metadataStores *memstore.Store[*driver.MetadataStore]
	tensorboards   *memstore.Store[*driver.Tensorboard]
	schedules      *memstore.Store[*driver.Schedule]
	nbTemplates    *memstore.Store[*driver.NotebookRuntimeTemplate]
	nbRuntimes     *memstore.Store[*driver.NotebookRuntime]

	operations *memstore.Store[*driver.Operation]

	monitoring mondriver.Monitoring
}

// New creates a new Vertex AI mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		opts:           opts,
		datasets:       memstore.New[*driver.Dataset](),
		models:         memstore.New[*driver.Model](),
		evaluations:    memstore.New[*driver.ModelEvaluation](),
		endpoints:      memstore.New[*driver.Endpoint](),
		customJobs:     memstore.New[*driver.CustomJob](),
		batchJobs:      memstore.New[*driver.BatchPredictionJob](),
		hpoJobs:        memstore.New[*driver.HyperparameterTuningJob](),
		trainPipes:     memstore.New[*driver.TrainingPipeline](),
		pipelineJobs:   memstore.New[*driver.PipelineJob](),
		tuningJobs:     memstore.New[*driver.TuningJob](),
		cachedContent:  memstore.New[*driver.CachedContent](),
		featurestores:  memstore.New[*driver.Featurestore](),
		entityTypes:    memstore.New[*driver.EntityType](),
		entityRecords:  memstore.New[[]driver.FeatureNameValue](),
		featureGroups:  memstore.New[*driver.FeatureGroup](),
		features:       memstore.New[*driver.Feature](),
		onlineStores:   memstore.New[*driver.FeatureOnlineStore](),
		featureViews:   memstore.New[*driver.FeatureView](),
		indexes:        memstore.New[*driver.Index](),
		indexEndpoints: memstore.New[*driver.IndexEndpoint](),
		metadataStores: memstore.New[*driver.MetadataStore](),
		tensorboards:   memstore.New[*driver.Tensorboard](),
		schedules:      memstore.New[*driver.Schedule](),
		nbTemplates:    memstore.New[*driver.NotebookRuntimeTemplate](),
		nbRuntimes:     memstore.New[*driver.NotebookRuntime](),
		operations:     memstore.New[*driver.Operation](),
	}
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// resName builds a Vertex resource name under the given location and collection.
func (m *Mock) resName(location, collection, id string) string {
	return "projects/" + m.opts.ProjectID + "/locations/" + orLocation(location) + "/" + collection + "/" + id
}

func orLocation(loc string) string {
	if loc == "" {
		return defaultLocation
	}

	return loc
}

func (*Mock) newID() string {
	return idgen.GenerateID("")
}

// doneOp records and returns an already-complete operation for the given
// location, carrying the response resource name in its metadata.
func (m *Mock) doneOp(location, resourceName string) *driver.Operation {
	name := "projects/" + m.opts.ProjectID + "/locations/" + orLocation(location) + "/operations/" + m.newID()
	op := &driver.Operation{
		Name:     name,
		Done:     true,
		Metadata: map[string]any{"target": resourceName},
		Response: map[string]any{"name": resourceName},
	}
	m.operations.Set(name, op)

	return op
}

// emitMetric pushes an auto-metric to the wired monitoring backend (no-op when
// monitoring is not set). Failures never affect the control plane.
func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "aiplatform.googleapis.com", MetricName: metricName, Value: value,
		Unit: "Count", Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

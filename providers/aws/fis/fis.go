// Package fis provides an in-memory mock implementation of the AWS Fault
// Injection Simulator (FIS) control plane: experiment templates and the
// experiments started from them.
//
// The mock is control-plane only — it does NOT inject any real faults. An
// experiment template is created synchronously with stable computed fields (id,
// arn, creationTime, lastUpdateTime) minted once at create and stored, so
// repeated reads never drift. StartExperiment materializes an experiment from a
// template and places it directly in the running state (there is no data plane
// to advance it to completion); StopExperiment moves a running experiment to the
// stopped terminal state.
package fis

import (
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// Compile-time check that Mock implements driver.FIS.
var _ driver.FIS = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// ARN resource kinds and the service marker.
const (
	kindTemplate   = "experiment-template"
	kindExperiment = "experiment"
	serviceName    = "fis"
)

// Experiment options defaults, applied when the caller supplies none, so reads
// are stable and match the real service.
const (
	accountTargetingSingle = "single-account"
	emptyResolutionFail    = "fail"
	actionsModeRunAll      = "run-all"
)

// Experiment and action state values.
const (
	statusRunning = "running"
	statusStopped = "stopped"

	reasonStarted = "Experiment started."
	reasonStopped = "Experiment stopped by user request."
)

// Mock is an in-memory implementation of the AWS FIS control plane. Templates
// are keyed by template id; experiments are keyed by experiment id.
type Mock struct {
	templates   *memstore.Store[driver.ExperimentTemplate]
	experiments *memstore.Store[driver.Experiment]
	opts        *config.Options
}

// New creates a new FIS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		templates:   memstore.New[driver.ExperimentTemplate](),
		experiments: memstore.New[driver.Experiment](),
		opts:        opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// templateID mints a stable FIS experiment-template id (e.g. "EXTa1b2c3d4").
func templateID() string {
	return "EXT" + strings.ToUpper(idgen.GenerateID(""))
}

// experimentID mints a stable FIS experiment id (e.g. "EXPa1b2c3d4").
func experimentID() string {
	return "EXP" + strings.ToUpper(idgen.GenerateID(""))
}

// templateARN mints the stable ARN reported for an experiment template.
func (m *Mock) templateARN(id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, kindTemplate+"/"+id)
}

// experimentARN mints the stable ARN reported for an experiment.
func (m *Mock) experimentARN(id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, kindExperiment+"/"+id)
}

// resourceRefFromARN extracts the (kind, id) pair from a FIS resource ARN. A
// template ARN of the form arn:aws:fis:{region}:{acct}:experiment-template/{id}
// yields (experiment-template, id); an experiment ARN yields (experiment, id).
func resourceRefFromARN(arn string) (kind, id string) {
	const arnParts = 6

	if !strings.Contains(arn, ":"+serviceName+":") {
		return "", ""
	}

	parts := strings.SplitN(arn, ":", arnParts)
	if len(parts) < arnParts {
		return "", ""
	}

	resource := parts[arnParts-1]

	slash := strings.IndexByte(resource, '/')
	if slash < 0 {
		return "", ""
	}

	return resource[:slash], resource[slash+1:]
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

func copyStopConditions(in []driver.StopCondition) []driver.StopCondition {
	if in == nil {
		return nil
	}

	return append([]driver.StopCondition(nil), in...)
}

func copyTarget(t *driver.Target) driver.Target {
	out := *t
	out.ResourceArns = copyStrings(t.ResourceArns)
	out.ResourceTags = copyTags(t.ResourceTags)
	out.Parameters = copyTags(t.Parameters)

	if t.Filters != nil {
		filters := make([]driver.TargetFilter, len(t.Filters))
		for i := range t.Filters {
			filters[i] = driver.TargetFilter{Path: t.Filters[i].Path, Values: copyStrings(t.Filters[i].Values)}
		}

		out.Filters = filters
	}

	return out
}

func copyTargets(in map[string]driver.Target) map[string]driver.Target {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.Target, len(in))

	for k := range in {
		v := in[k]
		out[k] = copyTarget(&v)
	}

	return out
}

func copyActions(in map[string]driver.Action) map[string]driver.Action {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.Action, len(in))

	for k, v := range in {
		a := v
		a.Parameters = copyTags(v.Parameters)
		a.Targets = copyTags(v.Targets)
		a.StartAfter = copyStrings(v.StartAfter)
		out[k] = a
	}

	return out
}

func copyLogConfiguration(in *driver.LogConfiguration) *driver.LogConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	if in.CloudWatchLogsConfiguration != nil {
		cw := *in.CloudWatchLogsConfiguration
		out.CloudWatchLogsConfiguration = &cw
	}

	if in.S3Configuration != nil {
		s3 := *in.S3Configuration
		out.S3Configuration = &s3
	}

	return &out
}

// copyTemplate returns an alias-free copy of a template so callers cannot mutate
// stored state through the result.
func copyTemplate(t *driver.ExperimentTemplate) driver.ExperimentTemplate {
	out := *t
	out.Actions = copyActions(t.Actions)
	out.Targets = copyTargets(t.Targets)
	out.StopConditions = copyStopConditions(t.StopConditions)
	out.LogConfiguration = copyLogConfiguration(t.LogConfiguration)
	out.Tags = copyTags(t.Tags)

	return out
}

// copyExperiment returns an alias-free copy of an experiment.
func copyExperiment(e *driver.Experiment) driver.Experiment {
	out := *e
	out.Targets = copyTargets(e.Targets)
	out.StopConditions = copyStopConditions(e.StopConditions)
	out.LogConfiguration = copyLogConfiguration(e.LogConfiguration)
	out.Tags = copyTags(e.Tags)

	if e.Actions != nil {
		actions := make(map[string]driver.ExperimentAction, len(e.Actions))

		for k := range e.Actions {
			a := e.Actions[k]
			a.Parameters = copyTags(a.Parameters)
			a.Targets = copyTags(a.Targets)
			a.StartAfter = copyStrings(a.StartAfter)
			actions[k] = a
		}

		out.Actions = actions
	}

	return out
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

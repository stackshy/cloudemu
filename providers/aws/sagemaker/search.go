package sagemaker

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const (
	maxFilterNameLen  = 255
	maxFilterValueLen = 1024
	searchTimeLayout  = "2006-01-02T15:04:05"
)

// Property kinds of a searchable field.
const (
	kindText   = 's'
	kindNumber = 'n'
	kindTime   = 't'
)

// sprop is one searchable property value.
type sprop struct {
	kind byte
	s    string
	n    float64
	t    time.Time
}

// srec is a resource flattened for search: filterable properties plus the
// record returned to the caller.
type srec struct {
	name   string
	props  map[string]sprop
	record map[string]any
	tags   []driver.Tag
	hyper  map[string]string
	params map[string]string
	nested map[string][]map[string]sprop
}

func newRec(name string) *srec {
	return &srec{name: name, props: map[string]sprop{}, record: map[string]any{}, nested: map[string][]map[string]sprop{}}
}

// str adds a text property (skipped when empty).
func (r *srec) str(key, v string) *srec {
	if v != "" {
		r.props[key] = sprop{kind: kindText, s: v}
		r.record[key] = v
	}

	return r
}

// num adds a numeric property.
func (r *srec) num(key string, v float64) *srec {
	r.props[key] = sprop{kind: kindNumber, n: v}
	r.record[key] = v

	return r
}

// at adds a time property from a stored RFC3339 string (skipped when empty).
func (r *srec) at(key, iso string) *srec {
	if iso != "" {
		r.props[key] = sprop{kind: kindTime, t: parseTime(iso)}
		r.record[key] = iso
	}

	return r
}

// withTags attaches the resource's live tags.
func (m *Mock) withTags(r *srec, arn string) *srec {
	r.tags, _ = m.tags.Get(arn)
	r.tags = copyTags(r.tags)

	return r
}

// searchAdapter flattens the stored resources of one search type.
type searchAdapter struct {
	known []string // the SDK property names a filter may use
	list  func() []*srec
}

// searchAdapters maps a search Resource to its adapter. Resource types that are
// valid in the API but have no backing data in the emulator (FeatureMetadata,
// ModelCard, PipelineVersion, Job, Image, ImageVersion) have no entry and
// search as empty.
func (m *Mock) searchAdapters() map[string]searchAdapter {
	return map[string]searchAdapter{
		driver.SearchTrainingJob: {
			known: []string{"TrainingJobName", "TrainingJobArn", "TrainingJobStatus", "SecondaryStatus", "RoleArn", "FailureReason", "CreationTime", "LastModifiedTime", "TrainingStartTime", "TrainingEndTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchTrainingJobs,
		},
		driver.SearchHyperParameterTuningJob: {
			known: []string{"HyperParameterTuningJobName", "HyperParameterTuningJobArn", "HyperParameterTuningJobStatus", "CreationTime", "LastModifiedTime", "HyperParameterTuningEndTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchTuningJobs,
		},
		driver.SearchEndpoint: {
			known: []string{"EndpointName", "EndpointArn", "EndpointConfigName", "EndpointStatus", "FailureReason", "CreationTime", "LastModifiedTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchEndpoints,
		},
		driver.SearchExperiment: {
			known: []string{"ExperimentName", "ExperimentArn", "DisplayName", "Description", "CreationTime", "LastModifiedTime"},
			list:  m.searchExperiments,
		},
		driver.SearchExperimentTrial: {
			known: []string{"TrialName", "TrialArn", "DisplayName", "ExperimentName", "CreationTime", "LastModifiedTime"},
			list:  m.searchTrials,
		},
		driver.SearchExperimentTrialComponent: {
			known: []string{"TrialComponentName", "TrialComponentArn", "DisplayName", "Status.PrimaryStatus", "StartTime", "EndTime", "CreationTime", "LastModifiedTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchTrialComponents,
		},
		driver.SearchModelPackage: {
			known: []string{"ModelPackageArn", "ModelPackageGroupName", "ModelPackageVersion", "ModelPackageStatus", "ModelApprovalStatus", "ModelPackageDescription", "CreationTime", "LastModifiedTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchModelPackages,
		},
		driver.SearchModelPackageGroup: {
			known: []string{"ModelPackageGroupName", "ModelPackageGroupArn", "ModelPackageGroupStatus", "ModelPackageGroupDescription", "CreationTime", "LastModifiedTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchPackageGroups,
		},
		driver.SearchPipeline: {
			known: []string{"PipelineName", "PipelineArn", "PipelineStatus", "RoleArn", "CreationTime", "LastModifiedTime"},
			list:  m.searchPipelines,
		},
		driver.SearchPipelineExecution: {
			known: []string{"PipelineExecutionArn", "PipelineExecutionStatus", "PipelineName", "CreationTime", "LastModifiedTime"},
			list:  m.searchExecutions,
		},
		driver.SearchFeatureGroup: {
			known: []string{"FeatureGroupName", "FeatureGroupArn", "RecordIdentifierFeatureName", "EventTimeFeatureName", "FeatureGroupStatus", "FailureReason", "CreationTime", "LastModifiedTime"}, //nolint:lll // SDK-shaped literal
			list:  m.searchFeatureGroups,
		},
		driver.SearchProject: {
			known: []string{"ProjectName", "ProjectArn", "ProjectId", "ProjectStatus", "ProjectDescription", "CreationTime", "LastModifiedTime"},
			list:  m.searchProjects,
		},
		driver.SearchModel: {
			known: []string{"ModelName", "ModelArn", "CreationTime", "LastModifiedTime"},
			list:  m.searchModels,
		},
	}
}

// searchResources is the full ResourceType enum.
var searchResources = map[string]bool{ //nolint:gochecknoglobals // enum values
	driver.SearchTrainingJob: true, driver.SearchExperiment: true, driver.SearchExperimentTrial: true,
	driver.SearchExperimentTrialComponent: true, driver.SearchEndpoint: true, driver.SearchModel: true,
	driver.SearchModelPackage: true, driver.SearchModelPackageGroup: true, driver.SearchPipeline: true,
	driver.SearchPipelineExecution: true, driver.SearchFeatureGroup: true, driver.SearchFeatureMetadata: true,
	driver.SearchImage: true, driver.SearchImageVersion: true, driver.SearchProject: true,
	driver.SearchHyperParameterTuningJob: true, driver.SearchModelCard: true, driver.SearchPipelineVersion: true, driver.SearchJob: true,
}

func (m *Mock) searchTrainingJobs() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, j := range m.trainingJobs.SortedValues() {
		o := m.observeTrainingJob(j)
		r := newRec(o.JobName).str("TrainingJobName", o.JobName).str("TrainingJobArn", o.JobARN).str("TrainingJobStatus", o.Status).
			str("SecondaryStatus", o.SecondaryStatus).str("RoleArn", o.RoleARN).str("FailureReason", o.FailureReason).
			at("CreationTime", o.CreationTime).at("LastModifiedTime", o.LastModifiedTime).
			at("TrainingStartTime", o.TrainingStartTime).at("TrainingEndTime", o.TrainingEndTime)
		r.hyper = copyStrMap(o.HyperParameters)

		channels := make([]map[string]sprop, 0, len(o.InputChannels))
		list := make([]map[string]any, 0, len(o.InputChannels))

		for _, c := range o.InputChannels {
			channels = append(channels, map[string]sprop{
				"InputDataConfig.ChannelName": {kind: kindText, s: c.Name}, "InputDataConfig.ContentType": {kind: kindText, s: c.ContentType},
			})

			list = append(list, map[string]any{"ChannelName": c.Name, "ContentType": c.ContentType})
		}

		r.nested["InputDataConfig"] = channels
		r.record["InputDataConfig"] = list
		r.record["HyperParameters"] = r.hyper

		out = append(out, m.withTags(r, o.JobARN))
	}

	return out
}

func (m *Mock) searchTuningJobs() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, j := range m.tuningJobs.SortedValues() {
		o := m.observeTuningJob(j)
		r := newRec(o.JobName).str("HyperParameterTuningJobName", o.JobName).str("HyperParameterTuningJobArn", o.JobARN).
			str("HyperParameterTuningJobStatus", o.Status).at("CreationTime", o.CreationTime).at("LastModifiedTime", o.LastModifiedTime).
			at("HyperParameterTuningEndTime", o.HPOJobEndTime)
		out = append(out, m.withTags(r, o.JobARN))
	}

	return out
}

func (m *Mock) searchEndpoints() []*srec {
	m.epMu.Lock()
	now := m.opts.Clock.Now()
	stored := m.endpoints.SortedValues()
	live := make([]*driver.Endpoint, 0, len(stored))

	for _, e := range stored {
		if o, ok := m.liveEndpoint(e.EndpointName, now); ok {
			live = append(live, o)
		}
	}
	m.epMu.Unlock()

	out := make([]*srec, 0, len(live))

	for _, e := range live {
		r := newRec(e.EndpointName).str("EndpointName", e.EndpointName).str("EndpointArn", e.EndpointARN).str("EndpointConfigName", e.ConfigName).
			str("EndpointStatus", e.Status).str("FailureReason", e.FailureReason).at("CreationTime", e.CreationTime).at("LastModifiedTime", e.LastModifiedTime) //nolint:lll // SDK-shaped literal
		out = append(out, m.withTags(r, e.EndpointARN))
	}

	return out
}

func (m *Mock) searchExperiments() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, e := range m.experiments.SortedValues() {
		r := newRec(e.ExperimentName).str("ExperimentName", e.ExperimentName).str("ExperimentArn", e.ExperimentARN).
			str("Description", e.Description).at("CreationTime", e.CreationTime).at("LastModifiedTime", e.CreationTime)
		out = append(out, m.withTags(r, e.ExperimentARN))
	}

	return out
}

func (m *Mock) searchTrials() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, t := range m.trials.SortedValues() {
		r := newRec(t.TrialName).str("TrialName", t.TrialName).str("TrialArn", t.TrialARN).str("ExperimentName", t.ExperimentName).
			at("CreationTime", t.CreationTime).at("LastModifiedTime", t.CreationTime)
		out = append(out, m.withTags(r, t.TrialARN))
	}

	return out
}

func (m *Mock) searchTrialComponents() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, tc := range m.trialComponents.SortedValues() {
		r := newRec(tc.Name).str("TrialComponentName", tc.Name).str("TrialComponentArn", tc.ARN).str("DisplayName", tc.DisplayName).
			at("StartTime", tc.StartTime).at("EndTime", tc.EndTime).at("CreationTime", tc.CreationTime).at("LastModifiedTime", tc.LastModifiedTime)

		if tc.Status != nil {
			r.str("Status.PrimaryStatus", tc.Status.PrimaryStatus)
		}

		r.params = map[string]string{}

		for k, p := range tc.Parameters {
			if p.StringValue != nil {
				r.params[k] = *p.StringValue
			} else if p.NumberValue != nil {
				r.params[k] = strconv.FormatFloat(*p.NumberValue, 'f', -1, 64)
			}
		}

		out = append(out, m.withTags(r, tc.ARN))
	}

	return out
}

func (m *Mock) searchModelPackages() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, p := range m.packages.SortedValues() {
		r := newRec(p.PackageARN).str("ModelPackageArn", p.PackageARN).str("ModelPackageGroupName", p.GroupName).num("ModelPackageVersion", float64(p.Version)). //nolint:lll // SDK-shaped literal
																						str("ModelPackageStatus", p.Status).str("ModelApprovalStatus", p.ApprovalStatus).str("ModelPackageDescription", p.Description). //nolint:lll // SDK-shaped; see nongoals/standards
																						at("CreationTime", p.CreationTime).at("LastModifiedTime", p.CreationTime)
		out = append(out, m.withTags(r, p.PackageARN))
	}

	return out
}

func (m *Mock) searchPackageGroups() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, g := range m.packageGroups.SortedValues() {
		r := newRec(g.GroupName).str("ModelPackageGroupName", g.GroupName).str("ModelPackageGroupArn", g.GroupARN).str("ModelPackageGroupStatus", g.Status). //nolint:lll // SDK-shaped literal
																					str("ModelPackageGroupDescription", g.Description).at("CreationTime", g.CreationTime).at("LastModifiedTime", g.CreationTime) //nolint:lll // SDK-shaped; see nongoals/standards
		out = append(out, m.withTags(r, g.GroupARN))
	}

	return out
}

func (m *Mock) searchPipelines() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, p := range m.pipelines.SortedValues() {
		r := newRec(p.PipelineName).str("PipelineName", p.PipelineName).str("PipelineArn", p.PipelineARN).str("PipelineStatus", p.Status).
			str("RoleArn", p.RoleARN).at("CreationTime", p.CreationTime).at("LastModifiedTime", p.LastModifiedTime)
		out = append(out, m.withTags(r, p.PipelineARN))
	}

	return out
}

func (m *Mock) searchExecutions() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, e := range m.executions.SortedValues() {
		modified := e.EndTime
		if modified == "" {
			modified = e.StartTime
		}

		r := newRec(e.ExecutionARN).str("PipelineExecutionArn", e.ExecutionARN).str("PipelineExecutionStatus", e.Status).str("PipelineName", e.PipelineName). //nolint:lll // SDK-shaped literal
																					at("CreationTime", e.StartTime).at("LastModifiedTime", modified)
		out = append(out, m.withTags(r, e.ExecutionARN))
	}

	return out
}

func (m *Mock) searchFeatureGroups() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, g := range m.featureGroups.SortedValues() {
		r := newRec(g.GroupName).str("FeatureGroupName", g.GroupName).str("FeatureGroupArn", g.GroupARN).str("RecordIdentifierFeatureName", g.RecordIdentifierName). //nolint:lll // SDK-shaped literal
																						str("EventTimeFeatureName", g.EventTimeFeatureName).str("FeatureGroupStatus", g.Status).str("FailureReason", g.FailureReason). //nolint:lll // SDK-shaped; see nongoals/standards
																						at("CreationTime", g.CreationTime).at("LastModifiedTime", g.CreationTime)
		out = append(out, m.withTags(r, g.GroupARN))
	}

	return out
}

func (m *Mock) searchProjects() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, p := range m.projects.SortedValues() {
		o := m.observeProject(p)
		r := newRec(o.Name).str("ProjectName", o.Name).str("ProjectArn", o.ARN).str("ProjectId", o.ID).str("ProjectStatus", o.Status).
			str("ProjectDescription", o.Description).at("CreationTime", o.CreationTime).at("LastModifiedTime", o.LastModifiedTime)
		out = append(out, m.withTags(r, o.ARN))
	}

	return out
}

func (m *Mock) searchModels() []*srec {
	var out []*srec //nolint:prealloc // SDK-shaped; see nongoals/standards

	for _, md := range m.models.SortedValues() {
		r := newRec(md.ModelName).str("ModelName", md.ModelName).str("ModelArn", md.ModelARN).at("CreationTime", md.CreationTime).at("LastModifiedTime", md.CreationTime) //nolint:lll // SDK-shaped literal
		r.record = map[string]any{"Model": r.record}
		out = append(out, m.withTags(r, md.ModelARN))
	}

	return out
}

// --- Expression validation and evaluation ---

var filterOperators = map[string]bool{ //nolint:gochecknoglobals // enum values
	"Equals": true, "NotEquals": true, "Exists": true, "NotExists": true, "GreaterThan": true, "GreaterThanOrEqualTo": true,
	"LessThan": true, "LessThanOrEqualTo": true, "In": true, "Contains": true,
}

var orderingOperators = map[string]bool{"GreaterThan": true, "GreaterThanOrEqualTo": true, "LessThan": true, "LessThanOrEqualTo": true} //nolint:gochecknoglobals,lll // enum values

// containsExempt lists, per resource, the properties that may use Contains any
// number of times (every other property is limited to one Contains per
// expression).
//
//nolint:gocyclo // validation mirrors the service rules
func containsExempt(resource, name string) bool {
	switch resource {
	case driver.SearchExperiment:
		return name == "DisplayName" || name == "ExperimentName" || strings.HasPrefix(name, "Tags") //nolint:goconst // SDK enum literals
	case driver.SearchExperimentTrial:
		return name == "DisplayName" || name == "TrialName" || strings.HasPrefix(name, "Tags")
	case driver.SearchExperimentTrialComponent:
		return name == "DisplayName" || name == "TrialComponentName" || strings.HasPrefix(name, "Tags") ||
			strings.HasPrefix(name, "InputArtifacts") || strings.HasPrefix(name, "OutputArtifacts")
	default:
		return false
	}
}

// searchCtx carries the per-request validation state.
type searchCtx struct {
	resource string
	known    map[string]bool
	contains int
	total    int
}

//nolint:gocyclo // validation mirrors the service rules
func (c *searchCtx) validate(e *driver.SearchExpression, depth int) error {
	if depth > driver.MaxSearchDepth {
		return errors.Newf(errors.InvalidArgument, "searchExpression nests deeper than %d levels", driver.MaxSearchDepth)
	}

	if len(e.Filters)+len(e.NestedFilters)+len(e.SubExpressions) == 0 {
		return errors.New(errors.InvalidArgument, "a search expression needs at least one of filters, nestedFilters and subExpressions")
	}

	for name, n := range map[string]int{"filters": len(e.Filters), "nestedFilters": len(e.NestedFilters), "subExpressions": len(e.SubExpressions)} { //nolint:lll // SDK-shaped literal
		if n > driver.MaxSearchListItems {
			return errors.Newf(errors.InvalidArgument, "%s must contain at most %d items", name, driver.MaxSearchListItems)
		}
	}

	if c.total += len(e.Filters) + len(e.NestedFilters) + len(e.SubExpressions); c.total > driver.MaxSearchExpression {
		return errors.Newf(errors.InvalidArgument, "a search expression has at most %d filters, nested filters and sub-expressions in total", driver.MaxSearchExpression) //nolint:lll // SDK-shaped literal
	}

	if e.Operator != "" && e.Operator != "And" && e.Operator != "Or" { //nolint:goconst // SDK enum literals
		return errors.Newf(errors.InvalidArgument, "operator %q must be And or Or", e.Operator)
	}

	for i := range e.Filters {
		if err := c.validateFilter(&e.Filters[i], ""); err != nil {
			return err
		}
	}

	for i := range e.NestedFilters {
		if err := c.validateNested(&e.NestedFilters[i]); err != nil {
			return err
		}
	}

	for i := range e.SubExpressions {
		if err := c.validate(&e.SubExpressions[i], depth+1); err != nil {
			return err
		}
	}

	return nil
}

func (c *searchCtx) validateNested(n *driver.SearchNested) error {
	if n.PropertyName == "" || len(n.Filters) == 0 || len(n.Filters) > driver.MaxSearchListItems {
		return errors.Newf(errors.InvalidArgument, "nestedFilters need a nestedPropertyName and 1-%d filters", driver.MaxSearchListItems)
	}

	for i := range n.Filters {
		if err := c.validateFilter(&n.Filters[i], n.PropertyName); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocyclo // validation mirrors the service rules
func (c *searchCtx) validateFilter(f *driver.SearchFilter, nested string) error {
	if f.Name == "" || len(f.Name) > maxFilterNameLen {
		return errors.Newf(errors.InvalidArgument, "a filter name must be 1-%d characters", maxFilterNameLen)
	}

	op := orDefault(f.Operator, "Equals")
	if !filterOperators[op] {
		return errors.Newf(errors.InvalidArgument, "operator %q is not a valid filter operator", f.Operator)
	}

	if op != "Exists" && op != "NotExists" && (f.Value == "" || len(f.Value) > maxFilterValueLen) { //nolint:goconst // SDK enum literals
		return errors.Newf(errors.InvalidArgument, "the value of filter %q must be 1-%d characters", f.Name, maxFilterValueLen)
	}

	if nested == "" && !c.knownName(f.Name) {
		return errors.Newf(errors.InvalidArgument, "Unknown property name: %s", f.Name)
	}

	if op == "Contains" && !containsExempt(c.resource, f.Name) { //nolint:goconst // SDK enum literals
		if c.contains++; c.contains > 1 { //nolint:gocritic // driver-signature params and SDK-shaped helpers
			return errors.New(errors.InvalidArgument, "'CONTAINS' operator usage limit of 1 exceeded.") //nolint:lll,revive // SDK-shaped; see nongoals/standards
		}
	}

	return nil
}

func (c *searchCtx) knownName(name string) bool {
	if c.known[name] || strings.HasPrefix(name, "Tags.") {
		return true
	}

	switch c.resource {
	case driver.SearchTrainingJob:
		return strings.HasPrefix(name, "HyperParameters.")
	case driver.SearchExperimentTrialComponent:
		return strings.HasPrefix(name, "Parameters.")
	default:
		return false
	}
}

// lookup returns a record's property by (possibly dotted) name.
func (r *srec) lookup(name string) (sprop, bool) {
	if p, ok := r.props[name]; ok {
		return p, true
	}

	switch {
	case strings.HasPrefix(name, "Tags."):
		for _, t := range r.tags {
			if t.Key == strings.TrimPrefix(name, "Tags.") {
				return sprop{kind: kindText, s: t.Value}, true
			}
		}

		return sprop{}, false
	case strings.HasPrefix(name, "HyperParameters."):
		return numberOrText(r.hyper, strings.TrimPrefix(name, "HyperParameters."))
	case strings.HasPrefix(name, "Parameters."):
		return numberOrText(r.params, strings.TrimPrefix(name, "Parameters."))
	default:
		p, ok := r.props[name]

		return p, ok
	}
}

// numberOrText reads a free-form property: numeric when its value parses as one.
func numberOrText(m map[string]string, key string) (sprop, bool) {
	v, ok := m[key]
	if !ok {
		return sprop{}, false
	}

	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return sprop{kind: kindNumber, n: n, s: v}, true
	}

	return sprop{kind: kindText, s: v}, true
}

func evalFilter(r *srec, f *driver.SearchFilter) (bool, error) {
	op := orDefault(f.Operator, "Equals")
	p, found := r.lookup(f.Name)

	switch op {
	case "Exists":
		return found, nil
	case "NotExists":
		return !found, nil
	case "NotEquals": //nolint:goconst // SDK enum literals
		if !found {
			return true, nil
		}
	default:
		if !found {
			return false, nil
		}
	}

	return compareProp(p, op, f)
}

//nolint:gocyclo // validation mirrors the service rules
func compareProp(p sprop, op string, f *driver.SearchFilter) (bool, error) {
	switch {
	case op == "In" || op == "Contains":
		if p.kind != kindText {
			return false, errors.Newf(errors.InvalidArgument, "operator %s is only supported for text properties (%s)", op, f.Name)
		}

		if op == "Contains" {
			return strings.Contains(p.s, f.Value), nil
		}

		for _, v := range strings.Split(f.Value, ",") {
			if strings.TrimSpace(v) == p.s {
				return true, nil
			}
		}

		return false, nil
	case p.kind == kindText:
		if orderingOperators[op] {
			return false, errors.Newf(errors.InvalidArgument, "operator %s is not supported for text properties (%s)", op, f.Name)
		}

		return (p.s == f.Value) == (op == "Equals"), nil //nolint:goconst // SDK enum literals
	case p.kind == kindNumber:
		want, err := strconv.ParseFloat(f.Value, 64)
		if err != nil {
			return false, errors.Newf(errors.InvalidArgument, "value %q of filter %s is not a number", f.Value, f.Name)
		}

		return cmpNumber(p.n, want, op), nil
	default:
		want, err := time.Parse(searchTimeLayout, strings.TrimSuffix(f.Value, "Z"))
		if err != nil {
			return false, errors.Newf(errors.InvalidArgument, "value %q of filter %s is not a timestamp (YYYY-mm-ddTHH:MM:SS)", f.Value, f.Name)
		}

		return cmpNumber(float64(p.t.UnixNano()), float64(want.UnixNano()), op), nil
	}
}

func cmpNumber(got, want float64, op string) bool {
	switch op {
	case "Equals":
		return got == want
	case "NotEquals":
		return got != want
	case "GreaterThan":
		return got > want
	case "GreaterThanOrEqualTo":
		return got >= want
	case "LessThan":
		return got < want
	default:
		return got <= want
	}
}

// evalExpr evaluates an expression: its filters, nested filters and
// sub-expressions are combined with the expression's Operator (And by default).
func evalExpr(r *srec, e *driver.SearchExpression) (bool, error) {
	or := e.Operator == "Or"
	result := !or

	combine := func(v bool) bool {
		if or {
			return result || v
		}

		return result && v
	}

	for i := range e.Filters {
		v, err := evalFilter(r, &e.Filters[i])
		if err != nil {
			return false, err
		}

		result = combine(v)
	}

	for i := range e.NestedFilters {
		v, err := evalNested(r, &e.NestedFilters[i])
		if err != nil {
			return false, err
		}

		result = combine(v)
	}

	for i := range e.SubExpressions {
		v, err := evalExpr(r, &e.SubExpressions[i])
		if err != nil {
			return false, err
		}

		result = combine(v)
	}

	return result, nil
}

// evalNested is true when some item of the list property satisfies every filter.
func evalNested(r *srec, n *driver.SearchNested) (bool, error) {
	items := r.nested[n.PropertyName]

	if n.PropertyName == "Tags" {
		for _, t := range r.tags {
			items = append(items, map[string]sprop{"Tags.Key": {kind: kindText, s: t.Key}, "Tags.Value": {kind: kindText, s: t.Value}, "Key": {kind: kindText, s: t.Key}, "Value": {kind: kindText, s: t.Value}}) //nolint:lll // SDK-shaped literal
		}
	}

	for _, item := range items {
		rec := &srec{props: item}

		match := true

		for i := range n.Filters {
			ok, err := evalFilter(rec, &n.Filters[i])
			if err != nil {
				return false, err
			}

			if !ok {
				match = false

				break
			}
		}

		if match {
			return true, nil
		}
	}

	return false, nil
}

// Search finds resources of one type matching an expression, sorted and paged.
//
//nolint:gocritic,gocyclo // validation mirrors the service rules
func (m *Mock) Search(_ context.Context, req driver.SearchRequest) (*driver.SearchResult, error) {
	if !searchResources[req.Resource] {
		return nil, errors.Newf(errors.InvalidArgument, "resource %q is not a valid search resource", req.Resource)
	}

	if req.MaxResults < 0 || req.MaxResults > driver.MaxListResults {
		return nil, errors.Newf(errors.InvalidArgument, "maxResults must be between 1 and %d", driver.MaxListResults)
	}

	adapter, backed := m.searchAdapters()[req.Resource]
	if !backed {
		return &driver.SearchResult{Hits: []driver.SearchHit{}}, nil
	}

	known := make(map[string]bool, len(adapter.known))
	for _, k := range adapter.known {
		known[k] = true
	}

	ctx := &searchCtx{resource: req.Resource, known: known}
	if req.Expression != nil {
		if err := ctx.validate(req.Expression, 1); err != nil {
			return nil, err
		}
	}

	sortBy := orDefault(req.SortBy, "LastModifiedTime")
	if !ctx.knownName(sortBy) {
		return nil, errors.Newf(errors.InvalidArgument, "Unknown property name: %s", sortBy)
	}

	matches := make([]*srec, 0)

	for _, r := range adapter.list() {
		ok := true

		if req.Expression != nil {
			var err error
			if ok, err = evalExpr(r, req.Expression); err != nil {
				return nil, err
			}
		}

		if ok {
			matches = append(matches, r)
		}
	}

	sortRecs(matches, sortBy, req.SortOrder != driver.SortAscending)

	size := req.MaxResults
	if size == 0 {
		size = driver.MaxListResults
	}

	page, err := pagination.Paginate(matches, req.NextToken, size)
	if err != nil {
		return nil, errors.New(errors.InvalidArgument, "Invalid value for NextToken")
	}

	res := &driver.SearchResult{Hits: make([]driver.SearchHit, 0, len(page.Items)), NextToken: page.NextPageToken, TotalHits: len(matches)}
	for _, r := range page.Items {
		res.Hits = append(res.Hits, driver.SearchHit{Resource: req.Resource, Record: recordOf(r)})
	}

	return res, nil
}

// recordOf returns the search record with the resource's live tags attached.
func recordOf(r *srec) map[string]any {
	rec := make(map[string]any, len(r.record)+1)
	for k, v := range r.record {
		rec[k] = v
	}

	tags := make([]map[string]any, 0, len(r.tags))
	for _, t := range r.tags {
		tags = append(tags, map[string]any{"Key": t.Key, "Value": t.Value})
	}

	if inner, ok := rec["Model"].(map[string]any); ok {
		inner["Tags"] = tags
	} else {
		rec["Tags"] = tags
	}

	return rec
}

func sortRecs(recs []*srec, by string, desc bool) {
	less := func(a, b *srec) bool {
		pa, _ := a.lookup(by)
		pb, _ := b.lookup(by)

		switch {
		case pa.kind == kindTime || pb.kind == kindTime:
			return pa.t.Before(pb.t)
		case pa.kind == kindNumber && pb.kind == kindNumber:
			return pa.n < pb.n
		default:
			return pa.s < pb.s
		}
	}

	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if less(a, b) == less(b, a) { // equal keys: stable by name
			return a.name < b.name
		}

		return less(a, b) != desc
	})
}

// GetSearchSuggestions suggests searchable HyperParameters and Tags property
// names (the ones the resources actually carry) beginning with the hint.
func (m *Mock) GetSearchSuggestions(_ context.Context, resource, hint string) ([]string, error) {
	if !searchResources[resource] {
		return nil, errors.Newf(errors.InvalidArgument, "resource %q is not a valid search resource", resource)
	}

	if len(hint) > driver.MaxSuggestionHint {
		return nil, errors.Newf(errors.InvalidArgument, "propertyNameHint must be at most %d characters", driver.MaxSuggestionHint)
	}

	adapter, backed := m.searchAdapters()[resource]
	if !backed {
		return []string{}, nil
	}

	names := map[string]bool{}

	for _, r := range adapter.list() {
		for _, t := range r.tags {
			names["Tags."+t.Key] = true
		}

		for k := range r.hyper {
			names["HyperParameters."+k] = true
		}

		for k := range r.params {
			names["Parameters."+k] = true
		}
	}

	out := make([]string, 0, len(names))

	for n := range names {
		if strings.HasPrefix(strings.ToLower(n), strings.ToLower(hint)) {
			out = append(out, n)
		}
	}

	sort.Strings(out)

	return out, nil
}

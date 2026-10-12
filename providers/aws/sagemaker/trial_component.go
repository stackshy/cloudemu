package sagemaker

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const (
	maxTrialComponentNameLen = 120
	maxParameterKeyLen       = 320
	maxArtifactKeyLen        = 128
	maxRemoveKeyLen          = 256
)

var trialComponentNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,119}$`) //nolint:gochecknoglobals // compiled once

// tcKey is the store key of a trial component: names are not case-sensitive.
func tcKey(name string) string { return strings.ToLower(name) }

func (m *Mock) trialComponentARN(name string) string {
	return m.arn("experiment-trial-component/" + name)
}

func cloneTrialComponent(in *driver.TrialComponent) *driver.TrialComponent {
	out := *in
	out.Parameters = copyParams(in.Parameters)
	out.InputArtifacts = copyArtifacts(in.InputArtifacts)
	out.OutputArtifacts = copyArtifacts(in.OutputArtifacts)
	out.TrialNames = append([]string(nil), in.TrialNames...)

	if in.Status != nil {
		s := *in.Status
		out.Status = &s
	}

	if in.Metadata != nil {
		md := *in.Metadata
		out.Metadata = &md
	}

	return &out
}

func copyParams(in map[string]driver.TrialComponentParameter) map[string]driver.TrialComponentParameter {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.TrialComponentParameter, len(in))

	for k, p := range in {
		if p.StringValue != nil {
			v := *p.StringValue
			p.StringValue = &v
		}

		if p.NumberValue != nil {
			v := *p.NumberValue
			p.NumberValue = &v
		}

		out[k] = p
	}

	return out
}

func copyArtifacts(in map[string]driver.TrialComponentArtifact) map[string]driver.TrialComponentArtifact {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.TrialComponentArtifact, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func validateStatus(s *driver.TrialComponentStatus) error {
	if s == nil {
		return nil
	}

	switch s.PrimaryStatus {
	case "", driver.TrialComponentInProgress, driver.TrialComponentCompleted, driver.TrialComponentFailed,
		driver.TrialComponentStopping, driver.TrialComponentStopped:
		return nil
	default:
		return errors.Newf(errors.InvalidArgument, "status.primaryStatus %q is not one of InProgress, Completed, Failed, Stopping, Stopped", s.PrimaryStatus)
	}
}

func validateParams(params map[string]driver.TrialComponentParameter) error {
	if len(params) > driver.MaxTrialComponentParameters {
		return errors.Newf(errors.ResourceExhausted, "a trial component has at most %d parameters", driver.MaxTrialComponentParameters)
	}

	for k, p := range params {
		if len(k) > maxParameterKeyLen {
			return errors.Newf(errors.InvalidArgument, "parameter key must be at most %d characters", maxParameterKeyLen)
		}

		if (p.StringValue == nil) == (p.NumberValue == nil) {
			return errors.Newf(errors.InvalidArgument, "parameter %q must set exactly one of StringValue and NumberValue", k)
		}
	}

	return nil
}

func validateArtifacts(kind string, arts map[string]driver.TrialComponentArtifact) error {
	if len(arts) > driver.MaxTrialComponentArtifacts {
		return errors.Newf(errors.ResourceExhausted, "a trial component has at most %d %s artifacts", driver.MaxTrialComponentArtifacts, kind)
	}

	for k, a := range arts {
		if len(k) > maxArtifactKeyLen || a.Value == "" {
			return errors.Newf(errors.InvalidArgument, "%s artifact %q needs a value and a key of at most %d characters", kind, k, maxArtifactKeyLen)
		}
	}

	return nil
}

func validateTrialComponentFields(name, display string, status *driver.TrialComponentStatus,
	params map[string]driver.TrialComponentParameter, in, out map[string]driver.TrialComponentArtifact,
) error {
	if name != "" && (len(name) > maxTrialComponentNameLen || !trialComponentNamePattern.MatchString(name)) {
		return errors.Newf(errors.InvalidArgument, "trialComponentName %q must be 1-%d letters, digits and hyphens", name, maxTrialComponentNameLen)
	}

	if display != "" && (len(display) > maxTrialComponentNameLen || !trialComponentNamePattern.MatchString(display)) {
		return errors.Newf(errors.InvalidArgument, "displayName %q must be 1-%d letters, digits and hyphens", display, maxTrialComponentNameLen)
	}

	if err := validateStatus(status); err != nil {
		return err
	}

	if err := validateParams(params); err != nil {
		return err
	}

	if err := validateArtifacts("input", in); err != nil {
		return err
	}

	return validateArtifacts("output", out)
}

// CreateTrialComponent creates a trial component. Names are unique per account
// and not case-sensitive.
func (m *Mock) CreateTrialComponent(_ context.Context, spec driver.TrialComponentSpec) (*driver.TrialComponent, error) {
	if spec.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "trialComponentName is required")
	}

	if err := validateTrialComponentFields(spec.Name, spec.DisplayName, spec.Status, spec.Parameters, spec.InputArtifacts, spec.OutputArtifacts); err != nil {
		return nil, err
	}

	if err := validateTags(spec.Tags); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.trialComponentARN(spec.Name)
	tc := &driver.TrialComponent{
		Name: spec.Name, ARN: arn, DisplayName: spec.DisplayName, StartTime: spec.StartTime, EndTime: spec.EndTime,
		Status: spec.Status, Parameters: spec.Parameters, InputArtifacts: spec.InputArtifacts, OutputArtifacts: spec.OutputArtifacts,
		Metadata: spec.Metadata, CreationTime: now, LastModifiedTime: now,
	}
	tc = cloneTrialComponent(tc)

	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	if !m.trialComponents.SetIfAbsent(tcKey(spec.Name), tc) {
		return nil, errors.Newf(errors.AlreadyExists, "trial component %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)

	return cloneTrialComponent(tc), nil
}

func (m *Mock) DescribeTrialComponent(_ context.Context, name string) (*driver.TrialComponent, error) {
	tc, ok := m.trialComponents.Get(tcKey(name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "trial component %q not found", name)
	}

	return cloneTrialComponent(tc), nil
}

// ListTrialComponents lists trial components, optionally those of one trial,
// one experiment (through its trials) or one source. The three filters are
// mutually exclusive.
func (m *Mock) ListTrialComponents(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.TrialComponent], error) {
	exp, trial, source := opts.Filters["ExperimentName"], opts.Filters["TrialName"], opts.Filters["SourceArn"]

	set := 0

	for _, f := range []string{exp, trial, source} {
		if f != "" {
			set++
		}
	}

	if set > 1 {
		return driver.ListPage[driver.TrialComponent]{}, errors.New(errors.InvalidArgument,
			"experimentName, trialName and sourceArn cannot be used together")
	}

	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	allowed, err := m.trialComponentScope(exp, trial)
	if err != nil {
		return driver.ListPage[driver.TrialComponent]{}, err
	}

	return listFrom(m.trialComponents, listSpec[driver.TrialComponent]{
		name:         func(t *driver.TrialComponent) string { return strings.ToLower(t.Name) },
		created:      func(t *driver.TrialComponent) string { return t.CreationTime },
		modified:     func(t *driver.TrialComponent) string { return t.LastModifiedTime },
		defaultOrder: driver.SortDescending,
		extra: func(t *driver.TrialComponent, _ map[string]string) bool {
			if source != "" && t.SourceARN != source {
				return false
			}

			return allowed == nil || allowed[tcKey(t.Name)]
		},
		clone: func(t *driver.TrialComponent) driver.TrialComponent { return *cloneTrialComponent(t) },
	}, opts)
}

// trialComponentScope returns the set of component keys an experiment or trial
// filter allows, nil when neither filter is set. The caller holds entityMu.
func (m *Mock) trialComponentScope(experiment, trial string) (map[string]bool, error) {
	switch {
	case trial != "":
		t, ok := m.trials.Get(trial)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "trial %q not found", trial)
		}

		return keysOf(t.TrialComponentNames), nil
	case experiment != "":
		if !m.experiments.Has(experiment) {
			return nil, errors.Newf(errors.NotFound, "experiment %q not found", experiment)
		}

		allowed := map[string]bool{}

		for _, t := range m.trials.All() {
			if t.ExperimentName == experiment {
				for _, c := range t.TrialComponentNames {
					allowed[tcKey(c)] = true
				}
			}
		}

		return allowed, nil
	default:
		return nil, nil //nolint:nilnil // no scope filter
	}
}

func keysOf(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[tcKey(n)] = true
	}

	return out
}

// UpdateTrialComponent merges the given parameters and artifacts into the
// component by key, removes the keys listed for removal and bumps
// LastModifiedTime.
func (m *Mock) UpdateTrialComponent(_ context.Context, up driver.TrialComponentUpdate) (*driver.TrialComponent, error) {
	display := ""
	if up.DisplayName != nil {
		display = *up.DisplayName
	}

	if err := validateTrialComponentFields("", display, up.Status, up.Parameters, up.InputArtifacts, up.OutputArtifacts); err != nil {
		return nil, err
	}

	for _, k := range append(append(append([]string(nil), up.ParametersToRemove...), up.InputArtifactsToRemove...), up.OutputArtifactsToRemove...) {
		if len(k) > maxRemoveKeyLen {
			return nil, errors.Newf(errors.InvalidArgument, "keys to remove must be at most %d characters", maxRemoveKeyLen)
		}
	}

	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	cur, ok := m.trialComponents.Get(tcKey(up.Name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "trial component %q not found", up.Name)
	}

	updated := cloneTrialComponent(cur)
	mergeTrialComponent(updated, &up)

	if err := validateTrialComponentFields("", "", nil, updated.Parameters, updated.InputArtifacts, updated.OutputArtifacts); err != nil {
		return nil, err
	}

	updated.LastModifiedTime = m.now()
	m.trialComponents.Set(tcKey(up.Name), updated)

	return cloneTrialComponent(updated), nil
}

func mergeTrialComponent(tc *driver.TrialComponent, up *driver.TrialComponentUpdate) {
	if up.DisplayName != nil {
		tc.DisplayName = *up.DisplayName
	}

	if up.StartTime != nil {
		tc.StartTime = *up.StartTime
	}

	if up.EndTime != nil {
		tc.EndTime = *up.EndTime
	}

	if up.Status != nil {
		s := *up.Status
		tc.Status = &s
	}

	if len(up.Parameters) > 0 || len(up.ParametersToRemove) > 0 {
		if tc.Parameters == nil {
			tc.Parameters = map[string]driver.TrialComponentParameter{}
		}

		for k, v := range copyParams(up.Parameters) {
			tc.Parameters[k] = v
		}

		for _, k := range up.ParametersToRemove {
			delete(tc.Parameters, k)
		}
	}

	tc.InputArtifacts = mergeArtifacts(tc.InputArtifacts, up.InputArtifacts, up.InputArtifactsToRemove)
	tc.OutputArtifacts = mergeArtifacts(tc.OutputArtifacts, up.OutputArtifacts, up.OutputArtifactsToRemove)
}

func mergeArtifacts(cur, add map[string]driver.TrialComponentArtifact, remove []string) map[string]driver.TrialComponentArtifact {
	if len(add) == 0 && len(remove) == 0 {
		return cur
	}

	if cur == nil {
		cur = map[string]driver.TrialComponentArtifact{}
	}

	for k, v := range add {
		cur[k] = v
	}

	for _, k := range remove {
		delete(cur, k)
	}

	return cur
}

// DeleteTrialComponent deletes a component that is not associated with any
// trial, and removes its lineage associations.
func (m *Mock) DeleteTrialComponent(_ context.Context, name string) (*driver.TrialComponent, error) {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	tc, ok := m.trialComponents.Get(tcKey(name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "trial component %q not found", name)
	}

	if len(tc.TrialNames) > 0 {
		return nil, errors.Newf(errors.FailedPrecondition,
			"trial component %q is associated with %d trial(s); disassociate it before deleting", name, len(tc.TrialNames))
	}

	m.trialComponents.Delete(tcKey(name))
	m.dropTags(tc.ARN)
	m.removeAssociationsOf(tc.ARN)

	return cloneTrialComponent(tc), nil
}

// AssociateTrialComponent links a component to a trial (both ends are kept in
// step). Associating an already-associated pair succeeds.
func (m *Mock) AssociateTrialComponent(_ context.Context, componentName, trialName string) (string, string, error) {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	tc, trial, err := m.componentAndTrial(componentName, trialName)
	if err != nil {
		return "", "", err
	}

	updatedTC, updatedTrial := cloneTrialComponent(tc), *trial
	updatedTC.TrialNames = addSorted(tc.TrialNames, trial.TrialName)
	updatedTrial.TrialComponentNames = addSorted(trial.TrialComponentNames, tc.Name)
	updatedTrial.Tags = copyTags(trial.Tags)
	m.trialComponents.Set(tcKey(componentName), updatedTC)
	m.trials.Set(trialName, &updatedTrial)

	return tc.ARN, trial.TrialARN, nil
}

// DisassociateTrialComponent removes the link; a missing link is a no-op.
func (m *Mock) DisassociateTrialComponent(_ context.Context, componentName, trialName string) (string, string, error) {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	tc, trial, err := m.componentAndTrial(componentName, trialName)
	if err != nil {
		return "", "", err
	}

	updatedTC, updatedTrial := cloneTrialComponent(tc), *trial
	updatedTC.TrialNames = removeName(tc.TrialNames, trial.TrialName)
	updatedTrial.TrialComponentNames = removeName(trial.TrialComponentNames, tc.Name)
	updatedTrial.Tags = copyTags(trial.Tags)
	m.trialComponents.Set(tcKey(componentName), updatedTC)
	m.trials.Set(trialName, &updatedTrial)

	return tc.ARN, trial.TrialARN, nil
}

// componentAndTrial fetches both ends of an association. The caller holds entityMu.
func (m *Mock) componentAndTrial(componentName, trialName string) (*driver.TrialComponent, *driver.Trial, error) {
	tc, ok := m.trialComponents.Get(tcKey(componentName))
	if !ok {
		return nil, nil, errors.Newf(errors.NotFound, "trial component %q not found", componentName)
	}

	trial, ok := m.trials.Get(trialName)
	if !ok {
		return nil, nil, errors.Newf(errors.NotFound, "trial %q not found", trialName)
	}

	return tc, trial, nil
}

func addSorted(in []string, name string) []string {
	out := append([]string(nil), in...)

	for _, n := range out {
		if strings.EqualFold(n, name) {
			return out
		}
	}

	out = append(out, name)
	sort.Strings(out)

	return out
}

func removeName(in []string, name string) []string {
	out := make([]string, 0, len(in))

	for _, n := range in {
		if !strings.EqualFold(n, name) {
			out = append(out, n)
		}
	}

	return out
}

package sfn

import (
	"context"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn/asl"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// validateASL runs the ASL parser's structural validation so CreateStateMachine
// rejects semantically-invalid definitions (unknown state Type, dangling Next,
// Choice without Choices, unsupported fields on Wait/Choice/Succeed/Fail,
// QueryLanguage JSONata) with InvalidDefinition, matching real Step Functions.
func validateASL(definition string) error {
	if _, err := asl.Parse(definition); err != nil {
		return invalidDefinition(err.Error())
	}

	return nil
}

func (m *Mock) getSM(arn string) (*smData, error) {
	if !validStateMachineARN(arn) {
		return nil, invalidArn("%q is not a valid state machine ARN", arn)
	}

	sd, ok := m.machines.Get(arn)
	if !ok {
		return nil, smNotFound(arn)
	}

	return sd, nil
}

// CreateStateMachine registers a new state machine. State machine names are
// unique per account/region, so a duplicate name is StateMachineAlreadyExists.
//
//nolint:gocritic // in is a value to satisfy the driver.SFN interface signature.
func (m *Mock) CreateStateMachine(
	ctx context.Context, in driver.CreateStateMachineInput,
) (arn, versionArn string, created time.Time, err error) {
	if in.Name == "" {
		return "", "", time.Time{}, invalidName("state machine name is required")
	}

	if in.Definition == "" {
		return "", "", time.Time{}, invalidName("state machine definition is required")
	}

	if err := validateASL(in.Definition); err != nil {
		return "", "", time.Time{}, err
	}

	// roleArn is required and must be a valid IAM role ARN. An empty or
	// malformed value is InvalidArn (real SFN validates the role ARN shape).
	if !validRoleARN(in.RoleArn) {
		return "", "", time.Time{}, invalidArn("%q is not a valid IAM role ARN", in.RoleArn)
	}

	smType := in.Type
	if smType == "" {
		smType = driver.TypeStandard
	}

	arn = m.smARN(regionctx.RegionOr(ctx, m.opts.Region), in.Name)
	now := m.now()
	sm := driver.StateMachine{
		ARN: arn, Name: in.Name, Definition: in.Definition, RoleArn: in.RoleArn,
		Type: smType, Status: driver.SMStatusActive, Description: in.Description,
		RevisionID: idgen.GenerateID(""), CreationDate: now, Tags: copyTags(in.Tags),
		LoggingConfigJSON: in.LoggingConfigJSON, TracingConfigJSON: in.TracingConfigJSON,
		EncryptionCfgJSON: in.EncryptionCfgJSON,
	}

	if in.Publish {
		versionArn = publishLocked(&sm, in.Description, now)
	}

	// Claim the name atomically so two concurrent same-name creates can't both
	// succeed (uniqueness invariant under concurrency).
	if !m.machines.SetIfAbsent(arn, &smData{sm: sm}) {
		return m.reconcileExisting(arn, &sm)
	}

	return arn, versionArn, now, nil
}

// reconcileExisting resolves a CreateStateMachine against a name that is already
// taken. Real Step Functions makes CreateStateMachine idempotent: a repeat call
// with the same name whose definition, type, logging and tracing configuration
// all match the existing machine returns that machine (HTTP 200) — a differing
// roleArn or tags is ignored. Any other difference is StateMachineAlreadyExists.
func (m *Mock) reconcileExisting(
	arn string, want *driver.StateMachine,
) (resolvedArn, versionArn string, created time.Time, err error) {
	sd, ok := m.machines.Get(arn)
	if !ok {
		return "", "", time.Time{}, smAlreadyExists(want.Name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	same := sd.sm.Definition == want.Definition &&
		sd.sm.Type == want.Type &&
		sd.sm.LoggingConfigJSON == want.LoggingConfigJSON &&
		sd.sm.TracingConfigJSON == want.TracingConfigJSON
	if !same {
		return "", "", time.Time{}, smAlreadyExists(want.Name)
	}

	return sd.sm.ARN, sd.sm.LatestVersionArn, sd.sm.CreationDate, nil
}

// publishLocked appends a new version to sm and returns its ARN. Callers must
// hold exclusive access to sm (it is only ever called on a not-yet-stored value
// or under the smData lock).
func publishLocked(sm *driver.StateMachine, description string, now time.Time) string {
	version := int64(len(sm.PublishedVersions) + 1)
	versionArn := sm.ARN + ":" + strconv.FormatInt(version, 10)
	sm.PublishedVersions = append(sm.PublishedVersions, driver.Version{
		ARN: versionArn, Description: description, Definition: sm.Definition,
		RoleArn: sm.RoleArn, RevisionID: sm.RevisionID, CreationDate: now,
	})
	sm.LatestVersionArn = versionArn

	return versionArn
}

func (m *Mock) DescribeStateMachine(_ context.Context, arn string) (*driver.StateMachine, error) {
	sd, err := m.getSM(arn)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := copySM(&sd.sm)

	return &out, nil
}

// hasUpdatableField reports whether an UpdateStateMachine request supplies at
// least one field it can mutate. Real SFN rejects an update carrying none of
// them with MissingRequiredParameter.
//
//nolint:gocritic // in is a value to satisfy the driver.SFN interface signature.
func hasUpdatableField(in driver.UpdateStateMachineInput) bool {
	return in.Definition != "" || in.RoleArn != "" || in.LoggingConfigJSON != "" ||
		in.TracingConfigJSON != "" || in.EncryptionCfgJSON != ""
}

//nolint:gocritic // in is a value to satisfy the driver.SFN interface signature.
func (m *Mock) UpdateStateMachine(
	_ context.Context, in driver.UpdateStateMachineInput,
) (*driver.UpdateStateMachineResult, error) {
	sd, err := m.getSM(in.ARN)
	if err != nil {
		return nil, err
	}

	// UpdateStateMachine must change at least one updatable field. Supplying
	// none is a no-op that real SFN rejects with MissingRequiredParameter (and
	// it must not bump the revision).
	if !hasUpdatableField(in) {
		return nil, missingRequiredParameter(
			"UpdateStateMachine requires at least one of definition or roleArn")
	}

	// A new definition must be structurally valid, matching real SFN's update-time
	// validation (and keeping run-time interpretation safe).
	if in.Definition != "" {
		if verr := validateASL(in.Definition); verr != nil {
			return nil, verr
		}
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	applyStateMachineUpdate(&sd.sm, in)

	now := m.now()
	sd.sm.RevisionID = idgen.GenerateID("")

	res := &driver.UpdateStateMachineResult{UpdateDate: now, RevisionID: sd.sm.RevisionID}
	if in.Publish {
		res.StateMachineVersionArn = publishLocked(&sd.sm, in.VersionDesc, now)
	}

	return res, nil
}

// applyStateMachineUpdate copies each supplied (non-empty) UpdateStateMachine
// field onto the stored state machine. The caller holds sm's write lock.
//
//nolint:gocritic // in is a value to satisfy the driver.SFN interface signature.
func applyStateMachineUpdate(sm *driver.StateMachine, in driver.UpdateStateMachineInput) {
	if in.Definition != "" {
		sm.Definition = in.Definition
	}

	if in.RoleArn != "" {
		sm.RoleArn = in.RoleArn
	}

	if in.LoggingConfigJSON != "" {
		sm.LoggingConfigJSON = in.LoggingConfigJSON
	}

	if in.TracingConfigJSON != "" {
		sm.TracingConfigJSON = in.TracingConfigJSON
	}

	if in.EncryptionCfgJSON != "" {
		sm.EncryptionCfgJSON = in.EncryptionCfgJSON
	}
}

func (m *Mock) DeleteStateMachine(_ context.Context, arn string) error {
	if !validStateMachineARN(arn) {
		return invalidArn("%q is not a valid state machine ARN", arn)
	}

	// DeleteStateMachine is idempotent in real SFN: deleting an absent machine
	// succeeds.
	m.machines.Delete(arn)

	return nil
}

func (m *Mock) ListStateMachines(_ context.Context) ([]driver.StateMachine, error) {
	all := m.machines.SortedValues()
	out := make([]driver.StateMachine, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		out = append(out, copySM(&sd.sm))
		sd.mu.RUnlock()
	}

	return out, nil
}

func copySM(s *driver.StateMachine) driver.StateMachine {
	out := *s
	out.Tags = copyTags(s.Tags)
	out.PublishedVersions = append([]driver.Version(nil), s.PublishedVersions...)

	return out
}

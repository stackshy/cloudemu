package sfn

import (
	"context"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

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
	_ context.Context, in driver.CreateStateMachineInput,
) (arn, versionArn string, created time.Time, err error) {
	if in.Name == "" {
		return "", "", time.Time{}, invalidName("state machine name is required")
	}

	if in.Definition == "" {
		return "", "", time.Time{}, invalidName("state machine definition is required")
	}

	smType := in.Type
	if smType == "" {
		smType = driver.TypeStandard
	}

	arn = m.smARN(in.Name)
	if m.machines.Has(arn) {
		return "", "", time.Time{}, smAlreadyExists(in.Name)
	}

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

	m.machines.Set(arn, &smData{sm: sm})

	return arn, versionArn, now, nil
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

//nolint:gocritic // in is a value to satisfy the driver.SFN interface signature.
func (m *Mock) UpdateStateMachine(
	_ context.Context, in driver.UpdateStateMachineInput,
) (*driver.UpdateStateMachineResult, error) {
	sd, err := m.getSM(in.ARN)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if in.Definition != "" {
		sd.sm.Definition = in.Definition
	}

	if in.RoleArn != "" {
		sd.sm.RoleArn = in.RoleArn
	}

	if in.LoggingConfigJSON != "" {
		sd.sm.LoggingConfigJSON = in.LoggingConfigJSON
	}

	if in.TracingConfigJSON != "" {
		sd.sm.TracingConfigJSON = in.TracingConfigJSON
	}

	if in.EncryptionCfgJSON != "" {
		sd.sm.EncryptionCfgJSON = in.EncryptionCfgJSON
	}

	now := m.now()
	sd.sm.RevisionID = idgen.GenerateID("")

	res := &driver.UpdateStateMachineResult{UpdateDate: now, RevisionID: sd.sm.RevisionID}
	if in.Publish {
		res.StateMachineVersionArn = publishLocked(&sd.sm, in.VersionDesc, now)
	}

	return res, nil
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

package sagemaker

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRoundTripSagemaker proves a snapshot/restore round-trip preserves
// the SageMaker mock's state under the original identities. It populates two
// distinct stores (models, experiments) via the public API, restores into a
// fresh mock, and asserts both the semantic reads and a byte-identical
// re-snapshot (which proves every populated store round-trips through the
// generic memstore machinery).
func TestSnapshotRoundTripSagemaker(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateModel(ctx, driver.ModelConfig{
		ModelName: "m-1",
		RoleARN:   "arn:aws:iam::123456789012:role/r",
		Tags:      []driver.Tag{{Key: "env", Value: "prod"}},
	})
	require.NoError(t, err)

	_, err = src.CreateExperiment(ctx, driver.ExperimentSpec{
		ExperimentName: "exp-1",
		Description:    "d",
	})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	model, err := dst.DescribeModel(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "m-1", model.ModelName)
	require.Len(t, model.Tags, 1)
	assert.Equal(t, "prod", model.Tags[0].Value)

	exp, err := dst.DescribeExperiment(ctx, "exp-1")
	require.NoError(t, err)
	assert.Equal(t, "exp-1", exp.ExperimentName)

	raw2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(raw, raw2), "re-snapshot must be byte-identical")
}

// TestSnapshot_ReflectionAllStores fails when a store field of Mock is missing
// from the persisted store list (a forgotten row would otherwise lose state on
// --persist restart with no other test noticing).
func TestSnapshot_ReflectionAllStores(t *testing.T) { // T15
	m := newTestMock()

	persisted := map[uintptr]string{}
	for _, s := range m.stores() {
		persisted[reflect.ValueOf(s.store).Pointer()] = s.key
	}

	v := reflect.ValueOf(m).Elem()
	typ := v.Type()
	found := 0

	for i := 0; i < typ.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.Ptr || !strings.Contains(f.Type().String(), "memstore.Store[") {
			continue
		}

		found++

		_, ok := persisted[f.Pointer()]
		assert.True(t, ok, "store field %s is not in Mock.stores()", typ.Field(i).Name)
	}

	assert.Equal(t, found, len(persisted), "stores() lists a store that is not a Mock field, or lists one twice")
	assert.Greater(t, found, 20)
}

// TestSnapshot_OldSnapshotFixtureLoads restores a snapshot shaped like one
// written before the new families existed: only the stores that existed then,
// second-precision times.
func TestSnapshot_OldSnapshotFixtureLoads(t *testing.T) { // T13, T14
	const old = `{
	  "trainingJobs": {"train-old": {"JobName":"train-old","JobARN":"arn:aws:sagemaker:us-east-1:123456789012:training-job/train-old",
	    "Status":"Completed","CreationTime":"2025-01-01T00:00:00Z","LastModifiedTime":"2025-01-01T00:00:00Z",
	    "TrainingStartTime":"2025-01-01T00:00:00Z","TrainingEndTime":"2025-01-01T00:00:00Z"}},
	  "models": {"m-old": {"ModelName":"m-old","ModelARN":"arn:aws:sagemaker:us-east-1:123456789012:model/m-old",
	    "CreationTime":"2025-01-01T00:00:00Z"}}
	}`

	m := newTestMock()
	require.NoError(t, m.Restore(context.Background(), json.RawMessage(old)))

	job, err := m.DescribeTrainingJob(context.Background(), "train-old")
	require.NoError(t, err)
	assert.Equal(t, "2025-01-01T00:00:00Z", job.CreationTime)

	page, err := m.ListTrainingJobs(context.Background(), driver.ListOptions{CreationTimeAfter: "2024-12-31T00:00:00Z"})
	require.NoError(t, err)
	assert.Len(t, page.Items, 1, "second-precision times still filter and sort")

	_, err = m.DescribeModel(context.Background(), "m-old")
	require.NoError(t, err)

	// a store the old snapshot did not have stays empty and usable
	eps, err := m.ListEndpoints(context.Background(), driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, eps.Items)
}

func TestSnapshot_ClusterOldInstanceCountJSON(t *testing.T) { // T51
	const old = `{"clusters": {"hp": {"ClusterName":"hp","ClusterARN":"arn","Status":"InService","CreationTime":"2025-01-01T00:00:00Z",
	  "InstanceGroups":[{"GroupName":"workers","InstanceType":"ml.g5.xlarge","InstanceCount":7,"ExecutionRole":"r"}]}}}`

	m := newTestMock()
	require.NoError(t, m.Restore(context.Background(), json.RawMessage(old)))

	c, err := m.DescribeCluster(context.Background(), "hp")
	require.NoError(t, err)
	require.Len(t, c.InstanceGroups, 1)
	assert.Equal(t, 7, c.InstanceGroups[0].TargetCount, "the old InstanceCount becomes the target")
	assert.Equal(t, 7, c.InstanceGroups[0].CurrentCount)

	page, err := m.ListClusterNodes(context.Background(), "hp", driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, page.Items, 7)
}

// TestSnapshot_RoundTripAllNewFamilies populates one resource of every family
// added for real-cloud parity, restores into a fresh Mock and requires each
// store's state to survive (byte-identical re-snapshot, Describe equal).
func TestSnapshot_RoundTripAllNewFamilies(t *testing.T) { // T16
	ctx := context.Background()
	src, fc := newTestMockClock()

	_, err := src.CreateProject(ctx, scSpec("proj-1"))
	require.NoError(t, err)
	_, err = src.CreateWorkforce(ctx, cognitoWorkforce("default"))
	require.NoError(t, err)
	_, err = src.CreateWorkteam(ctx, driver.WorkteamSpec{Name: "t1", Description: "d", Members: []driver.MemberDefinition{cognitoMember("g")}})
	require.NoError(t, err)
	_, err = src.CreateImage(ctx, driver.ImageSpec{Name: "img", RoleARN: testRole})
	require.NoError(t, err)
	_, err = src.CreateImageVersion(ctx, versionSpec("img", "tok-1", "stable"))
	require.NoError(t, err)
	_, err = src.CreateAppImageConfig(ctx, driver.AppImageConfigSpec{Name: "cfg-1"})
	require.NoError(t, err)

	_, err = src.CreateExperiment(ctx, driver.ExperimentSpec{ExperimentName: "exp"})
	require.NoError(t, err)
	_, err = src.CreateTrial(ctx, driver.TrialSpec{TrialName: "tr", ExperimentName: "exp"})
	require.NoError(t, err)
	_, err = src.CreateTrialComponent(ctx, driver.TrialComponentSpec{Name: "run-1"})
	require.NoError(t, err)

	data, err := src.CreateArtifact(ctx, artifactSpec("data-1", "s3://b/data"))
	require.NoError(t, err)
	act, err := src.CreateAction(ctx, driver.LineageEntitySpec{Name: "train", Type: "ModelTraining", Source: driver.LineageSource{SourceURI: "s3://a"}})
	require.NoError(t, err)
	_, err = src.CreateContext(ctx, driver.LineageEntitySpec{Name: "ctx", Type: "Endpoint", Source: driver.LineageSource{SourceURI: "arn:x"}})
	require.NoError(t, err)
	require.NoError(t, src.AddAssociation(ctx, data.ARN, act.ARN, driver.AssociationContributedTo))

	_, err = src.CreateMonitoringJobDefinition(ctx, defSpec(driver.MonitoringKinds[0], "def-1"))
	require.NoError(t, err)
	_, err = src.CreateMonitoringSchedule(ctx, schedSpec("sched-1", hourly()))
	require.NoError(t, err)
	fc.Advance(3 * time.Hour)

	_, err = src.CreateModelPackageGroup(ctx, driver.ModelPackageGroupSpec{GroupName: "grp"})
	require.NoError(t, err)
	_, err = src.PutModelPackageGroupPolicy(ctx, "grp", `{"Version":"2012-10-17","Statement":[]}`)
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	var snap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &snap))

	for _, key := range []string{
		"projects", "workforces", "workteams", "images", "imageVersions", "appImageConfigs", "trials", "trialComponents",
		"lineageEntities", "associations", "monDefs", "monSchedules", "packagePolicies",
	} {
		assert.Greater(t, len(snap[key]), 2, "store %s must hold state in the snapshot", key)
	}

	dst, dfc := newTestMockClock()
	dfc.Advance(3 * time.Hour) // the restored mock reads at the same instant as the source
	require.NoError(t, dst.Restore(ctx, raw))

	raw2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(raw, raw2), "re-snapshot must be byte-identical")

	wantProj, err := src.DescribeProject(ctx, "proj-1")
	require.NoError(t, err)
	gotProj, err := dst.DescribeProject(ctx, "proj-1")
	require.NoError(t, err)
	assert.Equal(t, wantProj, gotProj)

	wantSched, err := src.DescribeMonitoringSchedule(ctx, "sched-1")
	require.NoError(t, err)
	gotSched, err := dst.DescribeMonitoringSchedule(ctx, "sched-1")
	require.NoError(t, err)
	assert.Equal(t, wantSched, gotSched)
}

// TestSnapshot_OldEndpointGetsVariantSummaries pins that an endpoint written by
// a build that stored only the config's variants still reports ProductionVariants.
func TestSnapshot_OldEndpointGetsVariantSummaries(t *testing.T) { // T14
	const old = `{
	  "endpoints": {"ep-old": {"EndpointName":"ep-old","EndpointARN":"arn:aws:sagemaker:us-east-1:123456789012:endpoint/ep-old",
	    "ConfigName":"cfg","Status":"InService","CreationTime":"2025-01-01T00:00:00Z","LastModifiedTime":"2025-01-01T00:00:00Z",
	    "Variants":[{"VariantName":"A","ModelName":"m","InstanceType":"ml.m5.large","InitialInstanceCount":2,"InitialVariantWeight":0.5}]}}
	}`

	m := newTestMock()
	ctx := context.Background()
	require.NoError(t, m.Restore(ctx, json.RawMessage(old)))

	ep, err := m.DescribeEndpoint(ctx, "ep-old")
	require.NoError(t, err)
	require.Len(t, ep.VariantSummaries, 1)
	assert.Equal(t, "A", ep.VariantSummaries[0].VariantName)
	assert.Equal(t, 2, ep.VariantSummaries[0].CurrentInstanceCount)
	assert.InDelta(t, 0.5, ep.VariantSummaries[0].DesiredWeight, 0)

	out, err := m.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep-old", Body: []byte("x")})
	require.NoError(t, err)
	assert.Equal(t, "A", out.InvokedVariant)
}

package sagemaker

import (
	"bytes"
	"context"
	"testing"

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

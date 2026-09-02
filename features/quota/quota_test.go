package quota_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/features/quota"
)

func TestNewAWSDefaultsSeedsQuotas(t *testing.T) {
	r := quota.NewAWSDefaults(nil)

	q, err := r.Get("lambda", "L-B99A9384")
	require.NoError(t, err)
	assert.Equal(t, float64(1000), q.Value)
	assert.Equal(t, float64(1000), q.DefaultValue)
	assert.True(t, q.Adjustable)
	assert.Equal(t, "Concurrent executions", q.QuotaName)

	// A global quota is flagged as such.
	iam, err := r.Get("iam", "L-FE177D64")
	require.NoError(t, err)
	assert.True(t, iam.GlobalQuota)
}

func TestGetUnknownQuota(t *testing.T) {
	r := quota.NewAWSDefaults(nil)

	_, err := r.Get("ec2", "L-NOPE")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestListFiltersByService(t *testing.T) {
	r := quota.NewAWSDefaults(nil)

	vpc := r.List("vpc")
	assert.Len(t, vpc, 3)

	// Results are sorted by quota code within a service.
	for i := 1; i < len(vpc); i++ {
		assert.LessOrEqual(t, vpc[i-1].QuotaCode, vpc[i].QuotaCode)
	}

	all := r.List("")
	assert.Greater(t, len(all), len(vpc))
}

func TestListDefaultsIgnoresOverride(t *testing.T) {
	r := quota.NewAWSDefaults(nil)

	_, err := r.SetOverride("s3", "L-DC2B2D3D", 999)
	require.NoError(t, err)

	defaults := r.ListDefaults("s3")
	require.Len(t, defaults, 1)
	assert.Equal(t, float64(100), defaults[0].Value, "defaults list reports the default, not the override")

	applied := r.List("s3")
	require.Len(t, applied, 1)
	assert.Equal(t, float64(999), applied[0].Value, "applied list reflects the override")
}

func TestSetInitializesAppliedValue(t *testing.T) {
	r := quota.New(nil)
	r.Set(&quota.Quota{ServiceCode: "svc", QuotaCode: "L-1", DefaultValue: 7, Adjustable: true})

	q, err := r.Get("svc", "L-1")
	require.NoError(t, err)
	assert.Equal(t, float64(7), q.Value, "applied value defaults to DefaultValue")
}

func TestDefaultIgnoresOverride(t *testing.T) {
	r := quota.NewAWSDefaults(nil)

	_, err := r.SetOverride("s3", "L-DC2B2D3D", 250)
	require.NoError(t, err)

	applied, err := r.Get("s3", "L-DC2B2D3D")
	require.NoError(t, err)
	assert.Equal(t, float64(250), applied.Value)

	def, err := r.Default("s3", "L-DC2B2D3D")
	require.NoError(t, err)
	assert.Equal(t, float64(100), def.Value, "Default reports the original default, not the override")
}

func TestSetOverrideErrors(t *testing.T) {
	r := quota.New(nil)
	r.Set(&quota.Quota{ServiceCode: "svc", QuotaCode: "L-fixed", DefaultValue: 1, Adjustable: false})

	_, err := r.SetOverride("svc", "L-missing", 10)
	assert.True(t, cerrors.IsNotFound(err))

	_, err = r.SetOverride("svc", "L-fixed", 10)
	assert.True(t, cerrors.IsInvalidArgument(err), "non-adjustable quota cannot be overridden")
}

func TestRequestIncreaseRecordsHistory(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	r := quota.NewAWSDefaults(clock)

	cr, err := r.RequestIncrease("ec2", "L-1216C47A", 64)
	require.NoError(t, err)
	assert.Equal(t, quota.StatusPending, cr.Status)
	assert.Equal(t, float64(64), cr.DesiredValue)
	assert.NotEmpty(t, cr.ID)
	assert.Equal(t, clock.Now(), cr.Created)

	// A second request returns a distinct id and sorts newest-first.
	cr2, err := r.RequestIncrease("ec2", "L-1216C47A", 128)
	require.NoError(t, err)
	assert.NotEqual(t, cr.ID, cr2.ID)

	hist := r.History("ec2", "")
	require.Len(t, hist, 2)
	assert.Equal(t, cr2.ID, hist[0].ID, "newest first")

	// Filtering by quota code narrows further; a different service is empty.
	assert.Len(t, r.History("ec2", "L-1216C47A"), 2)
	assert.Empty(t, r.History("lambda", ""))
}

func TestRequestIncreaseValidation(t *testing.T) {
	r := quota.New(nil)
	r.Set(&quota.Quota{ServiceCode: "svc", QuotaCode: "L-adj", DefaultValue: 10, Adjustable: true})
	r.Set(&quota.Quota{ServiceCode: "svc", QuotaCode: "L-fixed", DefaultValue: 10, Adjustable: false})

	_, err := r.RequestIncrease("svc", "L-missing", 20)
	assert.True(t, cerrors.IsNotFound(err))

	_, err = r.RequestIncrease("svc", "L-fixed", 20)
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Desired must exceed the applied value.
	_, err = r.RequestIncrease("svc", "L-adj", 5)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

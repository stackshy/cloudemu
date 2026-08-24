package notificationhubs

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzureSASRuleKeysDeterministic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	first, err := m.PutSASRule(ctx, "my-ns", "sender", driver.AzureSASRule{Rights: []string{"Send"}})
	require.NoError(t, err)
	assert.NotEmpty(t, first.PrimaryKey)
	assert.NotEmpty(t, first.SecondaryKey)
	assert.NotEqual(t, first.PrimaryKey, first.SecondaryKey)

	// Updating rights must preserve the generated keys.
	updated, err := m.PutSASRule(ctx, "my-ns", "sender", driver.AzureSASRule{Rights: []string{"Send", "Listen"}})
	require.NoError(t, err)
	assert.Equal(t, first.PrimaryKey, updated.PrimaryKey)
	assert.Equal(t, []string{"Send", "Listen"}, updated.Rights)

	got, err := m.GetSASRule(ctx, "my-ns", "sender")
	require.NoError(t, err)
	assert.Equal(t, first.PrimaryKey, got.PrimaryKey)

	rules, err := m.ListSASRules(ctx, "my-ns")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Contains(t, rules, "sender")

	require.NoError(t, m.DeleteSASRule(ctx, "my-ns", "sender"))
	_, err = m.GetSASRule(ctx, "my-ns", "sender")
	assert.Error(t, err)
}

func TestAzureNamespaceMeta(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	meta, err := m.GetNamespaceMeta(ctx, "absent")
	require.NoError(t, err)
	assert.Empty(t, meta.SKU)

	require.NoError(t, m.SetNamespaceMeta(ctx, "my-ns", driver.AzureNamespaceMeta{SKU: "Standard"}))

	meta, err = m.GetNamespaceMeta(ctx, "my-ns")
	require.NoError(t, err)
	assert.Equal(t, "Standard", meta.SKU)
}

func TestAzureRegistrationLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	reg, err := m.CreateRegistration(ctx, "my-ns/hub1", driver.AzureRegistration{
		Platform: "GcmRegistrationDescription",
		Handle:   "token-1",
		Body:     "<GcmRegistrationId>token-1</GcmRegistrationId>",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, reg.RegistrationID)
	assert.Equal(t, "1", reg.ETag)

	got, err := m.GetRegistration(ctx, "my-ns/hub1", reg.RegistrationID)
	require.NoError(t, err)
	assert.Equal(t, "token-1", got.Handle)

	// A registration under a different hub key must be isolated.
	_, err = m.GetRegistration(ctx, "my-ns/hub2", reg.RegistrationID)
	assert.Error(t, err)

	list, err := m.ListRegistrations(ctx, "my-ns/hub1")
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, m.DeleteRegistration(ctx, "my-ns/hub1", reg.RegistrationID))
	_, err = m.GetRegistration(ctx, "my-ns/hub1", reg.RegistrationID)
	assert.Error(t, err)
}

package functions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestCreateAppServicePlanDefaults(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	plan, err := m.CreateAppServicePlan(ctx, AppServicePlan{Name: "consumption"})
	require.NoError(t, err)

	// Real Azure fills in the Consumption defaults when the SKU is omitted.
	assert.Equal(t, "consumption", plan.Name)
	assert.Equal(t, "Y1", plan.SKUName)
	assert.Equal(t, "Dynamic", plan.SKUTier)
	assert.Equal(t, 1, plan.Capacity)
	assert.NotEmpty(t, plan.ID)
	assert.Contains(t, plan.ID, "Microsoft.Web/serverfarms/consumption")
	assert.NotEmpty(t, plan.Location)
}

func TestCreateAppServicePlanEmptyName(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAppServicePlan(ctx, AppServicePlan{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestListAppServicePlansRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty", func(t *testing.T) {
		plans, err := m.ListAppServicePlans(ctx, "", "")
		require.NoError(t, err)
		assert.Empty(t, plans)
	})

	t.Run("explicit values round-trip", func(t *testing.T) {
		_, err := m.CreateAppServicePlan(ctx, AppServicePlan{
			Name:     "premium",
			Location: "westus2",
			SKUName:  "P1v3",
			SKUTier:  "PremiumV3",
			Kind:     "linux",
			Capacity: 3,
			Tags:     map[string]string{"env": "prod"},
		})
		require.NoError(t, err)

		plans, err := m.ListAppServicePlans(ctx, "", "")
		require.NoError(t, err)
		require.Len(t, plans, 1)

		got := plans[0]
		assert.Equal(t, "premium", got.Name)
		assert.Equal(t, "westus2", got.Location)
		assert.Equal(t, "P1v3", got.SKUName)
		assert.Equal(t, "PremiumV3", got.SKUTier)
		assert.Equal(t, "linux", got.Kind)
		assert.Equal(t, 3, got.Capacity)
		assert.Equal(t, "prod", got.Tags["env"])
	})
}

// TestAppServicePlanResourceGroupIsolation covers the deep-sweep finding: two
// resource groups (even in the same subscription) each naming a plan
// "default" must not collide, and neither Get nor Delete nor a resource-group
// scoped List may see the other resource group's plan.
func TestAppServicePlanResourceGroupIsolation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAppServicePlan(ctx, AppServicePlan{
		Name: "default", Subscription: "sub1", ResourceGroup: "rgA", SKUName: "B1",
	})
	require.NoError(t, err)

	_, err = m.CreateAppServicePlan(ctx, AppServicePlan{
		Name: "default", Subscription: "sub1", ResourceGroup: "rgB", SKUName: "P1v3",
	})
	require.NoError(t, err)

	inA, err := m.GetAppServicePlan(ctx, "sub1", "rgA", "default")
	require.NoError(t, err)
	assert.Equal(t, "B1", inA.SKUName)

	inB, err := m.GetAppServicePlan(ctx, "sub1", "rgB", "default")
	require.NoError(t, err)
	assert.Equal(t, "P1v3", inB.SKUName)

	listA, err := m.ListAppServicePlans(ctx, "sub1", "rgA")
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, "B1", listA[0].SKUName)

	// Deleting rgA's plan must not touch rgB's same-named plan.
	require.NoError(t, m.DeleteAppServicePlan(ctx, "sub1", "rgA", "default"))

	_, err = m.GetAppServicePlan(ctx, "sub1", "rgA", "default")
	require.True(t, cerrors.IsNotFound(err))

	stillThere, err := m.GetAppServicePlan(ctx, "sub1", "rgB", "default")
	require.NoError(t, err)
	assert.Equal(t, "P1v3", stillThere.SKUName)

	// Deleting an already-gone (or never-scoped) plan is NotFound.
	require.True(t, cerrors.IsNotFound(m.DeleteAppServicePlan(ctx, "sub1", "rgA", "default")))
}

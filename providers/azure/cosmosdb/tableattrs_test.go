package cosmosdb

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTableAttributes_Defaults verifies an un-seeded table returns the common
// defaults (GlobalDocumentDB / Standard offer) so a cost discoverer always sees
// a valid account shape.
func TestTableAttributes_Defaults(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	attrs, err := m.TableAttributes(ctx, "never-seeded")
	require.NoError(t, err)

	assert.Equal(t, "GlobalDocumentDB", attrs.Kind)
	assert.Equal(t, "Standard", attrs.OfferType)
	assert.False(t, attrs.EnableFreeTier)
	assert.Empty(t, attrs.Capabilities)
}

// TestTableAttributes_RoundTrip verifies fully-populated seeded attributes are
// returned unchanged through TableAttributes.
func TestTableAttributes_RoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	m.SetTableAttributes("events", driver.AccountAttributes{
		Kind:           "MongoDB",
		OfferType:      "Standard",
		EnableFreeTier: true,
		Capabilities:   []string{"EnableServerless", "EnableMongo"},
	})

	attrs, err := m.TableAttributes(ctx, "events")
	require.NoError(t, err)

	assert.Equal(t, "MongoDB", attrs.Kind)
	assert.Equal(t, "Standard", attrs.OfferType)
	assert.True(t, attrs.EnableFreeTier)
	assert.Equal(t, []string{"EnableServerless", "EnableMongo"}, attrs.Capabilities)
}

// TestTableAttributes_PartialSeedFillsDefaults verifies a partial seed keeps the
// provided fields and fills the empty Kind/OfferType with defaults, while
// preserving the cost flags exactly as seeded.
func TestTableAttributes_PartialSeedFillsDefaults(t *testing.T) {
	ctx := context.Background()

	t.Run("empty kind and offer type fall back to defaults", func(t *testing.T) {
		m := newTestMock()
		m.SetTableAttributes("t", driver.AccountAttributes{
			EnableFreeTier: true,
			Capabilities:   []string{"EnableServerless"},
		})

		attrs, err := m.TableAttributes(ctx, "t")
		require.NoError(t, err)

		assert.Equal(t, "GlobalDocumentDB", attrs.Kind)
		assert.Equal(t, "Standard", attrs.OfferType)
		assert.True(t, attrs.EnableFreeTier)
		assert.Equal(t, []string{"EnableServerless"}, attrs.Capabilities)
	})

	t.Run("only kind set keeps kind and defaults the offer type", func(t *testing.T) {
		m := newTestMock()
		m.SetTableAttributes("t", driver.AccountAttributes{Kind: "MongoDB"})

		attrs, err := m.TableAttributes(ctx, "t")
		require.NoError(t, err)

		assert.Equal(t, "MongoDB", attrs.Kind)
		assert.Equal(t, "Standard", attrs.OfferType)
	})

	t.Run("only offer type set keeps offer type and defaults the kind", func(t *testing.T) {
		m := newTestMock()
		m.SetTableAttributes("t", driver.AccountAttributes{OfferType: "Standard"})

		attrs, err := m.TableAttributes(ctx, "t")
		require.NoError(t, err)

		assert.Equal(t, "GlobalDocumentDB", attrs.Kind)
		assert.Equal(t, "Standard", attrs.OfferType)
	})
}

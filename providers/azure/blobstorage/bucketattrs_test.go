package blobstorage

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBucketAttributes_Defaults verifies an un-seeded bucket returns the
// real-Azure defaults (Standard_LRS / StorageV2 / Hot) so a cost discoverer
// always sees a priceable SKU.
func TestBucketAttributes_Defaults(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	attrs, err := m.BucketAttributes(ctx, "never-seeded")
	require.NoError(t, err)

	assert.Equal(t, "Standard_LRS", attrs.SKU)
	assert.Equal(t, "StorageV2", attrs.Kind)
	assert.Equal(t, "Hot", attrs.AccessTier)
}

// TestBucketAttributes_RoundTrip verifies fully-populated seeded attributes are
// returned unchanged through BucketAttributes.
func TestBucketAttributes_RoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	m.SetBucketAttributes("mybucket", driver.AccountAttributes{
		SKU:        "Premium_LRS",
		Kind:       "BlockBlobStorage",
		AccessTier: "Cool",
	})

	attrs, err := m.BucketAttributes(ctx, "mybucket")
	require.NoError(t, err)

	assert.Equal(t, "Premium_LRS", attrs.SKU)
	assert.Equal(t, "BlockBlobStorage", attrs.Kind)
	assert.Equal(t, "Cool", attrs.AccessTier)
}

// TestBucketAttributes_PartialSeedFillsDefaults verifies that a partial seed
// keeps the provided fields and fills the empty ones with defaults.
func TestBucketAttributes_PartialSeedFillsDefaults(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		seed           driver.AccountAttributes
		wantSKU        string
		wantKind       string
		wantAccessTier string
	}{
		{
			name:           "only SKU set",
			seed:           driver.AccountAttributes{SKU: "Premium_LRS"},
			wantSKU:        "Premium_LRS",
			wantKind:       "StorageV2",
			wantAccessTier: "Hot",
		},
		{
			name:           "only kind set",
			seed:           driver.AccountAttributes{Kind: "BlobStorage"},
			wantSKU:        "Standard_LRS",
			wantKind:       "BlobStorage",
			wantAccessTier: "Hot",
		},
		{
			name:           "only access tier set",
			seed:           driver.AccountAttributes{AccessTier: "Cool"},
			wantSKU:        "Standard_LRS",
			wantKind:       "StorageV2",
			wantAccessTier: "Cool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			m.SetBucketAttributes("b", tt.seed)

			attrs, err := m.BucketAttributes(ctx, "b")
			require.NoError(t, err)

			assert.Equal(t, tt.wantSKU, attrs.SKU)
			assert.Equal(t, tt.wantKind, attrs.Kind)
			assert.Equal(t, tt.wantAccessTier, attrs.AccessTier)
		})
	}
}

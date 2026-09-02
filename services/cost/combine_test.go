package cost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedSource is a Commitments source returning a fixed set regardless of at.
type fixedSource []Commitment

func (f fixedSource) ListActive(_ context.Context, _ time.Time) ([]Commitment, error) {
	return f, nil
}

// errSource is a Commitments source that always errors.
type errSource struct{}

func (errSource) ListActive(_ context.Context, _ time.Time) ([]Commitment, error) {
	return nil, errors.New("boom")
}

func TestCombineUnionsSourcesSorted(t *testing.T) {
	ri := fixedSource{{ID: "ri-2", Kind: KindReservedInstance, HourlyCommitmentUSD: 2}}
	sp := fixedSource{{ID: "sp-1", Kind: KindSavingsPlan, HourlyCommitmentUSD: 1}}

	got, err := Combine(ri, sp).ListActive(context.Background(), time.Now())
	require.NoError(t, err)
	require.Len(t, got, 2)

	// Union is ID-sorted for deterministic output regardless of source order.
	assert.Equal(t, "ri-2", got[0].ID)
	assert.Equal(t, "sp-1", got[1].ID)
}

func TestCombineSkipsNilSources(t *testing.T) {
	sp := fixedSource{{ID: "sp-1", Kind: KindSavingsPlan}}

	got, err := Combine(nil, sp, nil).ListActive(context.Background(), time.Now())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sp-1", got[0].ID)
}

func TestCombineEmptyIsEmpty(t *testing.T) {
	got, err := Combine().ListActive(context.Background(), time.Now())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCombinePropagatesError(t *testing.T) {
	got, err := Combine(fixedSource{{ID: "a"}}, errSource{}).ListActive(context.Background(), time.Now())
	require.Error(t, err)
	assert.Nil(t, got)
}

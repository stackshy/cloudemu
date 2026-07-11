package azuresearch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/azuresearch"
	"github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	provsearch "github.com/stackshy/cloudemu/providers/azure/azuresearch"
	"github.com/stackshy/cloudemu/recorder"
)

func newPortable(opts ...azuresearch.Option) *azuresearch.AzureSearch {
	o := config.NewOptions(config.WithAccountID("sub-1"))

	return azuresearch.New(provsearch.New(o), opts...)
}

func TestPortableRecordsAndCountsCalls(t *testing.T) {
	rec := recorder.New()
	col := metrics.NewCollector()
	a := newPortable(azuresearch.WithRecorder(rec), azuresearch.WithMetrics(col))

	_, err := a.CreateService(context.Background(), driver.ServiceConfig{Name: "s", ResourceGroup: "rg", Location: "eastus"})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCountFor("azuresearch", "CreateService"))
	assert.NotEmpty(t, col.All())
}

func TestPortableErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	boom := errors.New("injected")
	inj.Set("azuresearch", "CreateOrUpdateIndex", boom, inject.Always{})

	a := newPortable(azuresearch.WithErrorInjection(inj))

	_, err := a.CreateOrUpdateIndex(context.Background(), "s", driver.Index{Name: "i"})
	require.ErrorIs(t, err, boom)
}

func TestPortableLatencyApplied(t *testing.T) {
	a := newPortable(azuresearch.WithLatency(20 * time.Millisecond))

	start := time.Now()
	_, err := a.ListServices(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

func TestPortableForwardsResults(t *testing.T) {
	a := newPortable()
	ctx := context.Background()

	_, err := a.CreateService(ctx, driver.ServiceConfig{Name: "s", ResourceGroup: "rg", Location: "eastus"})
	require.NoError(t, err)

	_, err = a.CreateOrUpdateIndex(ctx, "s", driver.Index{
		Name: "products", Fields: []driver.Field{{Name: "id", Type: "Edm.String", Key: true}},
	})
	require.NoError(t, err)

	_, err = a.IndexDocuments(ctx, "s", "products", []driver.IndexAction{
		{Action: "upload", Document: map[string]any{"id": "1", "name": "alpha widget"}},
	})
	require.NoError(t, err)

	resp, err := a.SearchDocuments(ctx, "s", "products", driver.SearchRequest{Search: "widget", Count: true})
	require.NoError(t, err)
	assert.EqualValues(t, 1, resp.Count)
	require.Len(t, resp.Results, 1)
}

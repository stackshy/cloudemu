package azureai_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	provazureai "github.com/stackshy/cloudemu/v2/providers/azure/azureai"
	"github.com/stackshy/cloudemu/v2/services/azureai"
	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

func newPortable(opts ...azureai.Option) *azureai.AzureAI {
	o := config.NewOptions(config.WithAccountID("sub-1"))

	return azureai.New(provazureai.New(o), opts...)
}

func TestPortableRecordsAndCountsCalls(t *testing.T) {
	rec := recorder.New()
	col := metrics.NewCollector()
	a := newPortable(azureai.WithRecorder(rec), azureai.WithMetrics(col))

	_, err := a.CreateAccount(context.Background(), driver.AccountConfig{
		Name: "ai", ResourceGroup: "rg", Location: "eastus", Kind: "AIServices",
	})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCountFor("azureai", "CreateAccount"))
	assert.NotEmpty(t, col.All())
}

func TestPortableErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	boom := errors.New("injected")
	inj.Set("azureai", "CreateMLWorkspace", boom, inject.Always{})

	a := newPortable(azureai.WithErrorInjection(inj))

	_, err := a.CreateMLWorkspace(context.Background(), driver.MLWorkspaceConfig{
		Name: "ws", ResourceGroup: "rg", Location: "eastus",
	})
	require.ErrorIs(t, err, boom)
}

func TestPortableLatencyApplied(t *testing.T) {
	a := newPortable(azureai.WithLatency(20 * time.Millisecond))

	start := time.Now()
	_, err := a.ListAccounts(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

func TestPortableForwardsResults(t *testing.T) {
	a := newPortable()
	ctx := context.Background()

	_, err := a.CreateAccount(ctx, driver.AccountConfig{Name: "ai", ResourceGroup: "rg", Location: "eastus", Kind: "OpenAI"})
	require.NoError(t, err)

	got, err := a.GetAccount(ctx, "rg", "ai")
	require.NoError(t, err)
	assert.Equal(t, "ai", got.Name)

	resp, err := a.ChatCompletions(ctx, "ai", "gpt4o", driver.ChatCompletionRequest{
		Messages: []driver.ChatMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Choices, 1)
}

package lambda

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func defaultFuncConfig() driver.FunctionConfig {
	return driver.FunctionConfig{
		Name:    "my-func",
		Runtime: "go1.x",
		Handler: "main",
		Memory:  128,
		Timeout: 30,
		Tags:    map[string]string{"env": "test"},
	}
}

func TestCreateFunction(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.FunctionConfig
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "success", cfg: defaultFuncConfig()},
		{
			name: "already exists",
			cfg:  defaultFuncConfig(),
			setup: func(m *Mock) {
				_, _ = m.CreateFunction(context.Background(), defaultFuncConfig())
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			info, err := m.CreateFunction(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "my-func", info.Name)
			assertEqual(t, "go1.x", info.Runtime)
			assertEqual(t, "main", info.Handler)
			assertEqual(t, 128, info.Memory)
			assertEqual(t, 30, info.Timeout)
			assertEqual(t, "Active", info.State)
			assertNotEmpty(t, info.ARN)
		})
	}
}

func TestDeleteFunction(t *testing.T) {
	tests := []struct {
		name      string
		funcName  string
		setup     func(m *Mock)
		expectErr bool
	}{
		{
			name:     "success",
			funcName: "my-func",
			setup: func(m *Mock) {
				_, _ = m.CreateFunction(context.Background(), defaultFuncConfig())
			},
		},
		{name: "not found", funcName: "nope", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			err := m.DeleteFunction(context.Background(), tc.funcName)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestGetFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("found", func(t *testing.T) {
		info, err := m.GetFunction(ctx, "my-func")
		requireNoError(t, err)
		assertEqual(t, "my-func", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetFunction(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestListFunctions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	fns, err := m.ListFunctions(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(fns))

	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	cfg2 := defaultFuncConfig()
	cfg2.Name = "other-func"
	_, _ = m.CreateFunction(ctx, cfg2)

	fns, err = m.ListFunctions(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(fns))
}

func TestUpdateFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("success", func(t *testing.T) {
		info, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{
			Runtime: "python3.9",
			Memory:  256,
		})
		requireNoError(t, err)
		assertEqual(t, "python3.9", info.Runtime)
		assertEqual(t, 256, info.Memory)
		// Handler should remain unchanged since empty
		assertEqual(t, "main", info.Handler)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.UpdateFunction(ctx, "nope", driver.FunctionConfig{Runtime: "x"})
		assertError(t, err, true)
	})
}

func TestInvokeFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("no handler echoes a success stub", func(t *testing.T) {
		// The emulator can't run an uploaded zip, so with no Go handler it
		// returns a 200 stub echoing the payload rather than a FunctionError
		// (issue #319) — invoke stays testable.
		out, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func",
			Payload:      []byte(`{"k":1}`),
		})
		requireNoError(t, err)
		assertEqual(t, 200, out.StatusCode)
		assertEqual(t, "", out.Error)
		assertEqual(t, `{"k":1}`, string(out.Payload))
	})

	t.Run("with handler success", func(t *testing.T) {
		m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("echo: " + string(payload)), nil
		})

		out, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func",
			Payload:      []byte("hello"),
		})
		requireNoError(t, err)
		assertEqual(t, 200, out.StatusCode)
		assertEqual(t, "echo: hello", string(out.Payload))
	})

	t.Run("handler returns error", func(t *testing.T) {
		m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, fmt.Errorf("handler failure")
		})

		out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func"})
		requireNoError(t, err)
		assertEqual(t, 500, out.StatusCode)
		assertEqual(t, "handler failure", out.Error)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "nope"})
		assertError(t, err, true)
	})
}

func TestRegisterHandlerBeforeCreate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Register handler before function exists
	m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("pre-registered"), nil
	})

	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte("x")})
	requireNoError(t, err)
	assertEqual(t, 200, out.StatusCode)
	assertEqual(t, "pre-registered", string(out.Payload))
}

func TestPublishVersion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("publish first version", func(t *testing.T) {
		ver, err := m.PublishVersion(ctx, "my-func", "first release")
		requireNoError(t, err)
		assertEqual(t, "my-func", ver.FunctionName)
		assertEqual(t, "1", ver.Version)
		assertEqual(t, "first release", ver.Description)
		assertNotEmpty(t, ver.CodeSHA256)
		assertNotEmpty(t, ver.CreatedAt)
	})

	t.Run("publish second version", func(t *testing.T) {
		ver, err := m.PublishVersion(ctx, "my-func", "second release")
		requireNoError(t, err)
		assertEqual(t, "2", ver.Version)
	})

	t.Run("list versions includes LATEST and published", func(t *testing.T) {
		versions, err := m.ListVersions(ctx, "my-func")
		requireNoError(t, err)
		assertEqual(t, 3, len(versions)) // $LATEST + 2 published
		assertEqual(t, "$LATEST", versions[0].Version)
		assertEqual(t, "1", versions[1].Version)
		assertEqual(t, "2", versions[2].Version)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.PublishVersion(ctx, "nope", "")
		assertError(t, err, true)
	})

	t.Run("list versions function not found", func(t *testing.T) {
		_, err := m.ListVersions(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestVersionImmutability(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	// Publish version with original config
	ver1, err := m.PublishVersion(ctx, "my-func", "v1")
	requireNoError(t, err)
	origSHA := ver1.CodeSHA256

	// Update the function code (CodeSha256 tracks the deployment package, not
	// configuration, matching real Lambda).
	_, err = m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{
		Runtime: "python3.9",
		Handler: "new_handler",
		Code:    []byte("new-deployment-package"),
	})
	requireNoError(t, err)

	// Publish new version - should have different SHA
	ver2, err := m.PublishVersion(ctx, "my-func", "v2")
	requireNoError(t, err)

	// The two versions should have different code SHAs since the code changed
	assertEqual(t, true, origSHA != ver2.CodeSHA256)

	// Verify that version list still has original version
	versions, err := m.ListVersions(ctx, "my-func")
	requireNoError(t, err)
	assertEqual(t, "1", versions[1].Version)
	assertEqual(t, origSHA, versions[1].CodeSHA256)
}

func TestCreateAlias(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")

	t.Run("create alias", func(t *testing.T) {
		alias, err := m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName:    "my-func",
			Name:            "prod",
			FunctionVersion: "1",
			Description:     "production alias",
		})
		requireNoError(t, err)
		assertEqual(t, "prod", alias.Name)
		assertEqual(t, "1", alias.FunctionVersion)
		assertEqual(t, "production alias", alias.Description)
		assertNotEmpty(t, alias.AliasARN)
	})

	t.Run("get alias", func(t *testing.T) {
		alias, err := m.GetAlias(ctx, "my-func", "prod")
		requireNoError(t, err)
		assertEqual(t, "prod", alias.Name)
		assertEqual(t, "1", alias.FunctionVersion)
	})

	t.Run("list aliases", func(t *testing.T) {
		aliases, err := m.ListAliases(ctx, "my-func")
		requireNoError(t, err)
		assertEqual(t, 1, len(aliases))
	})

	t.Run("alias already exists", func(t *testing.T) {
		_, err := m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName:    "my-func",
			Name:            "prod",
			FunctionVersion: "1",
		})
		assertError(t, err, true)
	})

	t.Run("version not found", func(t *testing.T) {
		_, err := m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName:    "my-func",
			Name:            "staging",
			FunctionVersion: "99",
		})
		assertError(t, err, true)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName: "nope",
			Name:         "x",
		})
		assertError(t, err, true)
	})
}

func TestUpdateAlias(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.PublishVersion(ctx, "my-func", "v2")
	_, _ = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "prod", FunctionVersion: "1",
	})

	t.Run("update to new version", func(t *testing.T) {
		alias, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName:    "my-func",
			Name:            "prod",
			FunctionVersion: "2",
			Description:     "updated",
		})
		requireNoError(t, err)
		assertEqual(t, "2", alias.FunctionVersion)
		assertEqual(t, "updated", alias.Description)
	})

	t.Run("alias not found", func(t *testing.T) {
		_, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "my-func", Name: "nope", FunctionVersion: "1",
		})
		assertError(t, err, true)
	})

	t.Run("function not found", func(t *testing.T) {
		_, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName: "nope", Name: "prod",
		})
		assertError(t, err, true)
	})

	t.Run("atomic: failed routing validation leaves alias unchanged", func(t *testing.T) {
		_, _ = m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName: "my-func", Name: "atomic", FunctionVersion: "1",
		})

		// A valid FunctionVersion change combined with a RoutingConfig that fails
		// validation ($LATEST additional version) must fail as a whole and leave
		// the alias completely unchanged — FunctionVersion must stay "1".
		_, err := m.UpdateAlias(ctx, driver.AliasConfig{
			FunctionName:    "my-func",
			Name:            "atomic",
			FunctionVersion: "2",
			RoutingConfig: &driver.AliasRoutingConfig{
				AdditionalVersionWeights: map[string]float64{"$LATEST": 0.1},
			},
		})
		assertError(t, err, true)

		got, err := m.GetAlias(ctx, "my-func", "atomic")
		requireNoError(t, err)
		assertEqual(t, "1", got.FunctionVersion)
		if got.RoutingConfig != nil {
			t.Fatalf("expected RoutingConfig to remain nil, got %+v", got.RoutingConfig)
		}
	})
}

func TestDeleteAlias(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "prod", FunctionVersion: "1",
	})

	t.Run("delete alias", func(t *testing.T) {
		err := m.DeleteAlias(ctx, "my-func", "prod")
		requireNoError(t, err)

		_, err = m.GetAlias(ctx, "my-func", "prod")
		assertError(t, err, true)
	})

	t.Run("alias not found", func(t *testing.T) {
		err := m.DeleteAlias(ctx, "my-func", "nope")
		assertError(t, err, true)
	})

	t.Run("function not found", func(t *testing.T) {
		err := m.DeleteAlias(ctx, "nope", "prod")
		assertError(t, err, true)
	})
}

func TestAliasWeightedRouting(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.PublishVersion(ctx, "my-func", "v2")

	alias, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "canary",
		FunctionVersion: "1",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 0.1},
		},
	})
	requireNoError(t, err)
	assertEqual(t, "1", alias.FunctionVersion)
	assertEqual(t, 0.1, alias.RoutingConfig.AdditionalVersionWeights["2"])

	// Update routing config
	updated, err := m.UpdateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func",
		Name:         "canary",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 0.5},
		},
	})
	requireNoError(t, err)
	assertEqual(t, 0.5, updated.RoutingConfig.AdditionalVersionWeights["2"])
}

func TestAliasWeightedRoutingRejectsLatest(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")

	// A weighted alias whose own FunctionVersion is $LATEST is rejected.
	_, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "bad",
		FunctionVersion: "$LATEST",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"1": 0.1},
		},
	})
	assertError(t, err, true)

	// A plain alias on $LATEST is allowed; adding routing later that leaves the
	// effective FunctionVersion at $LATEST is rejected.
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "plain",
		FunctionVersion: "$LATEST",
	})
	requireNoError(t, err)

	_, err = m.UpdateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func",
		Name:         "plain",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"1": 0.1},
		},
	})
	assertError(t, err, true)
}

// TestAliasWeightedRoutingRejectsOutOfBoundsWeights proves each additional
// version weight must lie in [0.0, 1.0] and the additional weights must sum to
// at most 1.0 — real Lambda rejects violations as InvalidParameterValueException.
func TestAliasWeightedRoutingRejectsOutOfBoundsWeights(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.PublishVersion(ctx, "my-func", "v2")

	// A weight above 1.0 is rejected.
	_, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "over", FunctionVersion: "1",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 1.5},
		},
	})
	assertError(t, err, true)

	// A negative weight is rejected.
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "neg", FunctionVersion: "1",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": -0.1},
		},
	})
	assertError(t, err, true)

	// Additional weights that sum above 1.0 are rejected on UpdateAlias too.
	_, err = m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "canary", FunctionVersion: "1",
	})
	requireNoError(t, err)

	_, err = m.UpdateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "canary",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 0.7, "1": 0.6},
		},
	})
	assertError(t, err, true)

	// A valid in-bounds weight is still accepted.
	alias, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "ok", FunctionVersion: "1",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 0.25},
		},
	})
	requireNoError(t, err)
	assertEqual(t, 0.25, alias.RoutingConfig.AdditionalVersionWeights["2"])
}

func TestAliasWeightedRoutingSplitsInvocations(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1") // version "1"
	_, _ = m.PublishVersion(ctx, "my-func", "v2") // version "2"

	_, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "canary",
		FunctionVersion: "1",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 0.3},
		},
	})
	requireNoError(t, err)

	const n = 2000

	counts := map[string]int{}

	for i := 0; i < n; i++ {
		out, invokeErr := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func",
			Qualifier:    "canary",
			Payload:      []byte(fmt.Sprintf("event-%d", i)),
		})
		requireNoError(t, invokeErr)
		counts[out.ExecutedVersion]++
	}

	// Weighted routing is deterministic in the payload, so this split is stable
	// across runs: both versions receive traffic and version 2 lands near its
	// configured 30% weight.
	if counts["1"] == 0 || counts["2"] == 0 {
		t.Fatalf("expected both versions to receive traffic, got %+v", counts)
	}

	frac := float64(counts["2"]) / float64(n)
	if frac < 0.25 || frac > 0.35 {
		t.Fatalf("version 2 share = %.3f, want ~0.30 (counts %+v)", frac, counts)
	}
}

func TestAliasWeightedRoutingIsDeterministicPerPayload(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.PublishVersion(ctx, "my-func", "v2")

	_, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "canary",
		FunctionVersion: "1",
		RoutingConfig: &driver.AliasRoutingConfig{
			AdditionalVersionWeights: map[string]float64{"2": 0.5},
		},
	})
	requireNoError(t, err)

	first, err := m.Invoke(ctx, driver.InvokeInput{
		FunctionName: "my-func", Qualifier: "canary", Payload: []byte("stable-key"),
	})
	requireNoError(t, err)

	for i := 0; i < 20; i++ {
		out, invokeErr := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func", Qualifier: "canary", Payload: []byte("stable-key"),
		})
		requireNoError(t, invokeErr)

		if out.ExecutedVersion != first.ExecutedVersion {
			t.Fatalf("same payload routed to %q then %q; must be deterministic",
				first.ExecutedVersion, out.ExecutedVersion)
		}
	}
}

func TestInvokeDryRunValidatesQualifier(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.CreateAlias(ctx, driver.AliasConfig{FunctionName: "my-func", Name: "prod", FunctionVersion: "1"})

	t.Run("valid alias returns 204 without running", func(t *testing.T) {
		out, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func", InvokeType: "DryRun", Qualifier: "prod",
		})
		requireNoError(t, err)
		assertEqual(t, 204, out.StatusCode)
		assertEqual(t, "1", out.ExecutedVersion)
	})

	t.Run("unknown alias is ResourceNotFound", func(t *testing.T) {
		_, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func", InvokeType: "DryRun", Qualifier: "missing",
		})
		assertError(t, err, true)
	})

	t.Run("unknown version is ResourceNotFound", func(t *testing.T) {
		_, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func", InvokeType: "DryRun", Qualifier: "99",
		})
		assertError(t, err, true)
	})
}

func TestPublishLayerVersion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	t.Run("publish first layer version", func(t *testing.T) {
		lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name:               "my-layer",
			Description:        "shared utilities",
			Content:            []byte("layer-content-v1"),
			CompatibleRuntimes: []string{"go1.x", "python3.9"},
		})
		requireNoError(t, err)
		assertEqual(t, "my-layer", lv.Name)
		assertEqual(t, 1, lv.Version)
		assertEqual(t, "shared utilities", lv.Description)
		assertEqual(t, int64(16), lv.ContentSize)
		assertNotEmpty(t, lv.ContentSHA256)
		assertNotEmpty(t, lv.ARN)
	})

	t.Run("publish second version", func(t *testing.T) {
		lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
			Name:    "my-layer",
			Content: []byte("layer-content-v2"),
		})
		requireNoError(t, err)
		assertEqual(t, 2, lv.Version)
	})

	t.Run("get layer version", func(t *testing.T) {
		lv, err := m.GetLayerVersion(ctx, "my-layer", 1)
		requireNoError(t, err)
		assertEqual(t, 1, lv.Version)
		assertEqual(t, "shared utilities", lv.Description)
	})

	t.Run("list layer versions", func(t *testing.T) {
		versions, err := m.ListLayerVersions(ctx, "my-layer")
		requireNoError(t, err)
		assertEqual(t, 2, len(versions))
	})

	t.Run("layer not found for get", func(t *testing.T) {
		_, err := m.GetLayerVersion(ctx, "nope", 1)
		assertError(t, err, true)
	})

	t.Run("version not found", func(t *testing.T) {
		_, err := m.GetLayerVersion(ctx, "my-layer", 99)
		assertError(t, err, true)
	})

	t.Run("list versions layer not found", func(t *testing.T) {
		_, err := m.ListLayerVersions(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestDeleteLayerVersion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer", Content: []byte("v1")})
	_, _ = m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer", Content: []byte("v2")})

	t.Run("delete specific version", func(t *testing.T) {
		err := m.DeleteLayerVersion(ctx, "layer", 1)
		requireNoError(t, err)

		_, err = m.GetLayerVersion(ctx, "layer", 1)
		assertError(t, err, true)

		// Version 2 should still exist
		lv, err := m.GetLayerVersion(ctx, "layer", 2)
		requireNoError(t, err)
		assertEqual(t, 2, lv.Version)
	})

	t.Run("layer not found", func(t *testing.T) {
		err := m.DeleteLayerVersion(ctx, "nope", 1)
		assertError(t, err, true)
	})

	t.Run("version not found", func(t *testing.T) {
		err := m.DeleteLayerVersion(ctx, "layer", 99)
		assertError(t, err, true)
	})
}

// TestDeleteLayerVersionDoesNotRenumber pins that a deleted layer version's
// number is never reused: the monotonic counter keeps incrementing regardless
// of deletions, matching real Lambda.
func TestDeleteLayerVersionDoesNotRenumber(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, _ = m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer", Content: []byte("v1")})
	v2, _ := m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer", Content: []byte("v2")})
	assertEqual(t, 2, v2.Version)

	requireNoError(t, m.DeleteLayerVersion(ctx, "layer", 1))

	versions, err := m.ListLayerVersions(ctx, "layer")
	requireNoError(t, err)
	assertEqual(t, 1, len(versions))
	assertEqual(t, 2, versions[0].Version)

	v3, err := m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer", Content: []byte("v3")})
	requireNoError(t, err)
	assertEqual(t, 3, v3.Version)
}

// TestPublishLayerVersionMetadata pins that CompatibleArchitectures and
// LicenseInfo round-trip through PublishLayerVersion/GetLayerVersion.
func TestPublishLayerVersionMetadata(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	published, err := m.PublishLayerVersion(ctx, driver.LayerConfig{
		Name:                    "my-layer",
		Content:                 []byte("v1"),
		CompatibleRuntimes:      []string{"python3.9"},
		CompatibleArchitectures: []string{"arm64"},
		LicenseInfo:             "MIT",
	})
	requireNoError(t, err)
	assertEqual(t, "MIT", published.LicenseInfo)
	assertEqual(t, 1, len(published.CompatibleArchitectures))
	assertEqual(t, "arm64", published.CompatibleArchitectures[0])

	got, err := m.GetLayerVersion(ctx, "my-layer", 1)
	requireNoError(t, err)
	assertEqual(t, "MIT", got.LicenseInfo)
	assertEqual(t, "arm64", got.CompatibleArchitectures[0])
}

// TestPublishLayerVersionConcurrentRaceFree hammers PublishLayerVersion for a
// single new layer name from many goroutines. Before the SetIfAbsent +
// per-layer mutex fix, two goroutines racing the first publish could both see
// the layer as absent and independently create a *layerData, losing whichever
// one wrote second and letting the version counter restart; concurrent
// nextVer++ on the surviving layerData could also duplicate a version number.
// Every published version must be unique and the count must match the number
// of successful publishes. Must run clean under `go test -race`.
func TestPublishLayerVersionConcurrentRaceFree(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	const workers = 20

	var wg sync.WaitGroup

	versions := make([]int, workers)

	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()

			lv, err := m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "shared-layer", Content: []byte("v")})
			if err != nil {
				return
			}

			versions[i] = lv.Version
		}(i)
	}

	wg.Wait()

	seen := make(map[int]bool, workers)
	for _, v := range versions {
		if seen[v] {
			t.Fatalf("duplicate published version %d", v)
		}

		seen[v] = true
	}

	assertEqual(t, workers, len(seen))

	all, err := m.ListLayerVersions(ctx, "shared-layer")
	requireNoError(t, err)
	assertEqual(t, workers, len(all))
}

func TestListLayers(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		layers, err := m.ListLayers(ctx)
		requireNoError(t, err)
		assertEqual(t, 0, len(layers))
	})

	t.Run("returns latest version per layer", func(t *testing.T) {
		_, _ = m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer-a", Content: []byte("a1")})
		_, _ = m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer-a", Content: []byte("a2")})
		_, _ = m.PublishLayerVersion(ctx, driver.LayerConfig{Name: "layer-b", Content: []byte("b1")})

		layers, err := m.ListLayers(ctx)
		requireNoError(t, err)
		assertEqual(t, 2, len(layers))
	})
}

func TestPutFunctionConcurrency(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	t.Run("set concurrency", func(t *testing.T) {
		err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
			FunctionName:                 "my-func",
			ReservedConcurrentExecutions: 100,
		})
		requireNoError(t, err)
	})

	t.Run("get concurrency", func(t *testing.T) {
		cfg, err := m.GetFunctionConcurrency(ctx, "my-func")
		requireNoError(t, err)
		assertEqual(t, 100, cfg.ReservedConcurrentExecutions)
		assertEqual(t, "my-func", cfg.FunctionName)
	})

	t.Run("delete concurrency", func(t *testing.T) {
		err := m.DeleteFunctionConcurrency(ctx, "my-func")
		requireNoError(t, err)

		_, err = m.GetFunctionConcurrency(ctx, "my-func")
		assertError(t, err, true)
	})

	t.Run("function not found set", func(t *testing.T) {
		err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{FunctionName: "nope"})
		assertError(t, err, true)
	})

	t.Run("function not found get", func(t *testing.T) {
		_, err := m.GetFunctionConcurrency(ctx, "nope")
		assertError(t, err, true)
	})

	t.Run("function not found delete", func(t *testing.T) {
		err := m.DeleteFunctionConcurrency(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestLambdaMetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	ctx := context.Background()

	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	m.RegisterHandler("my-func", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	t.Run("invoke emits metrics", func(t *testing.T) {
		_, err := m.Invoke(ctx, driver.InvokeInput{
			FunctionName: "my-func",
			Payload:      []byte("test"),
		})
		requireNoError(t, err)

		result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "AWS/Lambda",
			MetricName: "Invocations",
			Dimensions: map[string]string{"FunctionName": "my-func"},
			StartTime:  fc.Now().Add(-1 * time.Hour),
			EndTime:    fc.Now().Add(1 * time.Hour),
			Period:     60,
			Stat:       "Sum",
		})
		requireNoError(t, err)
		assertEqual(t, true, len(result.Values) > 0)
	})

	t.Run("error invoke emits error metrics", func(t *testing.T) {
		m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, fmt.Errorf("fail")
		})

		_, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func"})
		requireNoError(t, err)

		result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "AWS/Lambda",
			MetricName: "Errors",
			Dimensions: map[string]string{"FunctionName": "my-func"},
			StartTime:  fc.Now().Add(-1 * time.Hour),
			EndTime:    fc.Now().Add(1 * time.Hour),
			Period:     60,
			Stat:       "Sum",
		})
		requireNoError(t, err)
		assertEqual(t, true, len(result.Values) > 0)
	})
}

func TestPublishVersionConcurrent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			_, _ = m.PublishVersion(ctx, "my-func", "concurrent")
		}()
	}

	wg.Wait()

	versions, err := m.ListVersions(ctx, "my-func")
	requireNoError(t, err)
	assertEqual(t, 11, len(versions)) // $LATEST + 10 published

	seen := make(map[string]bool)
	for _, v := range versions {
		assertEqual(t, false, seen[v.Version])
		seen[v.Version] = true
	}
}

func TestAliasRoutingConfigDeepCopy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.PublishVersion(ctx, "my-func", "v2")

	rc := &driver.AliasRoutingConfig{
		AdditionalVersionWeights: map[string]float64{"2": 0.5},
	}

	_, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "deep-copy",
		FunctionVersion: "1",
		RoutingConfig:   rc,
	})
	requireNoError(t, err)

	// Mutate the original config's map: the stored alias must be unaffected.
	rc.AdditionalVersionWeights["2"] = 0.9

	alias, err := m.GetAlias(ctx, "my-func", "deep-copy")
	requireNoError(t, err)
	assertEqual(t, 0.5, alias.RoutingConfig.AdditionalVersionWeights["2"])
}

// TestConcurrentAliasRaceFree hammers a single alias with concurrent
// UpdateAlias/GetAlias/ListAliases calls. Every one of these paths reads or
// mutates fields on the shared *aliasData, so without per-entity
// synchronization the -race detector flags a data race on alias.FunctionVersion
// / Description / RoutingConfig. It must run clean under `go test -race`.
func TestConcurrentAliasRaceFree(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_, _ = m.CreateFunction(ctx, defaultFuncConfig())
	_, _ = m.PublishVersion(ctx, "my-func", "v1")
	_, _ = m.PublishVersion(ctx, "my-func", "v2")

	_, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName:    "my-func",
		Name:            "prod",
		FunctionVersion: "1",
	})
	requireNoError(t, err)

	const (
		workers    = 8
		iterations = 40
	)

	var wg sync.WaitGroup

	run := func(fn func()) {
		defer wg.Done()

		for i := 0; i < iterations; i++ {
			fn()
		}
	}

	wg.Add(4 * workers)

	for w := 0; w < workers; w++ {
		go run(func() {
			_, _ = m.UpdateAlias(ctx, driver.AliasConfig{
				FunctionName: "my-func", Name: "prod", FunctionVersion: "1", Description: "a",
			})
		})
		go run(func() {
			_, _ = m.UpdateAlias(ctx, driver.AliasConfig{
				FunctionName: "my-func", Name: "prod", FunctionVersion: "2", Description: "b",
			})
		})
		go run(func() { _, _ = m.GetAlias(ctx, "my-func", "prod") })
		go run(func() { _, _ = m.ListAliases(ctx, "my-func") })
	}

	wg.Wait()

	alias, err := m.GetAlias(ctx, "my-func", "prod")
	requireNoError(t, err)

	if alias.FunctionVersion != "1" && alias.FunctionVersion != "2" {
		t.Fatalf("alias left in inconsistent version %q", alias.FunctionVersion)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}

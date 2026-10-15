package virtualmachines

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func TestKeyPairsAndScaleSetsSameNameInTwoGroups(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	for _, rg := range []string{"rg1", "rg2"} {
		tags := map[string]string{subTag: "s1", rgTag: rg}
		if _, err := src.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "k", Tags: tags}); err != nil {
			t.Fatalf("key in %s: %v", rg, err)
		}

		set := ScaleSet{Name: "ss", Subscription: "s1", ResourceGroup: rg, Location: rg}
		if _, err := src.CreateScaleSet(ctx, set); err != nil {
			t.Fatalf("scale set in %s: %v", rg, err)
		}
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	for _, rg := range []string{"rg1", "rg2"} {
		kp, err := dst.GetKeyPairScoped(ctx, "s1", rg, "k")
		if err != nil || kp.Tags[rgTag] != rg {
			t.Errorf("key in %s after restore = %v, %v", rg, kp, err)
		}

		set, err := dst.UpdateScaleSet(ctx, "s1", rg, "ss", ScaleSetPatch{Tags: map[string]string{"rg": rg}})
		if err != nil || set.Location != rg {
			t.Errorf("scale set in %s after restore = %v, %v", rg, set, err)
		}
	}

	if err := dst.DeleteKeyPairScoped(ctx, "s1", "rg1", "k"); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.GetKeyPairScoped(ctx, "s1", "rg2", "k"); err != nil {
		t.Errorf("rg2 key gone after rg1 delete: %v", err)
	}

	if err := dst.DeleteScaleSet(ctx, "s1", "rg1", "ss"); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.ListScaleSetVMs(ctx, "s1", "rg2", "ss"); err != nil {
		t.Errorf("rg2 scale set gone after rg1 delete: %v", err)
	}
}

// A snapshot taken before key pairs and scale sets were keyed by scope stores
// them by name; restore files them under their recorded scope.
func TestRestoreMigratesNameKeyedComputeRecords(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	m.keyPairs.Set("k", &driver.KeyPairInfo{Name: "k", Tags: map[string]string{rgTag: "rg1"}})
	m.scaleSets.Set("ss", &ScaleSet{Name: "ss", Subscription: "s1", ResourceGroup: "rg1"})

	data, err := m.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if _, err := dst.GetKeyPairScoped(ctx, dst.opts.AccountID, "rg1", "k"); err != nil {
		t.Errorf("legacy key not migrated: %v", err)
	}

	if _, ok := dst.scaleSets.Get(scaleSetKey("s1", "rg1", "ss")); !ok {
		t.Error("legacy scale set not migrated")
	}
}

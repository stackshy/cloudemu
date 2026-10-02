package functions

import (
	"context"
	"encoding/json"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestSiteConfigBlobsAndPolicies(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	created, err := m.UpsertSiteMeta(ctx, SiteMeta{
		Name: "app1", Subscription: "sub1", ResourceGroup: "rgA",
		SiteConfig: json.RawMessage(`{"http20Enabled":true}`),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if created.PublishingPassword == "" {
		t.Fatal("publishing password not minted on create")
	}

	if _, err := m.SetSiteConfigBlob(ctx, "sub1", "RGA", "app1", "ConnectionStrings",
		json.RawMessage(`{"db":{"value":"x","type":"Custom"}}`)); err != nil {
		t.Fatalf("set blob: %v", err)
	}

	if _, err := m.SetPublishingPolicy(ctx, "sub1", "rgA", "app1", "FTP", false); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	got, err := m.GetSiteMeta(ctx, "sub1", "rgA", "app1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if string(got.Configs["connectionstrings"]) != `{"db":{"value":"x","type":"Custom"}}` {
		t.Fatalf("connectionstrings = %s", got.Configs["connectionstrings"])
	}

	if allow, ok := got.PublishingPolicies["ftp"]; !ok || allow {
		t.Fatalf("ftp policy = %v, %v; want false", allow, ok)
	}

	// The returned copy must not alias the stored maps or bytes.
	got.Configs["connectionstrings"][0] = 'X'
	got.PublishingPolicies["ftp"] = true
	got.SiteConfig[0] = 'X'

	again, _ := m.GetSiteMeta(ctx, "sub1", "rgA", "app1")
	if again.Configs["connectionstrings"][0] != '{' || again.PublishingPolicies["ftp"] || again.SiteConfig[0] != '{' {
		t.Fatal("GetSiteMeta returned aliased state")
	}

	// A re-PUT of the site keeps the generated password and the config documents.
	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "app1", Subscription: "sub1", ResourceGroup: "rgA"}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	again, _ = m.GetSiteMeta(ctx, "sub1", "rgA", "app1")
	if again.PublishingPassword != created.PublishingPassword || again.Configs["connectionstrings"] == nil {
		t.Fatal("re-PUT lost the password or config documents")
	}

	if _, err := m.SetSiteConfigBlob(ctx, "sub1", "rgB", "app1", "logs", json.RawMessage(`{}`)); !cerrors.IsNotFound(err) {
		t.Fatalf("wrong resource group: err = %v, want NotFound", err)
	}
}

func TestIsSiteNameTaken(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	if m.IsSiteNameTaken(ctx, "app1") {
		t.Fatal("empty store reports name taken")
	}

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "app1", Subscription: "sub1", ResourceGroup: "rgA"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, name := range []string{"app1", "APP1"} {
		if !m.IsSiteNameTaken(ctx, name) {
			t.Fatalf("%s: want taken", name)
		}
	}
}

func TestSiteConfigSnapshotRoundTrip(t *testing.T) {
	m := newMetaMock()
	ctx := context.Background()

	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{
		Name: "app1", Subscription: "sub1", ResourceGroup: "rgA",
		SiteConfig: json.RawMessage(`{"http20Enabled":true}`),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := m.SetSiteConfigBlob(ctx, "sub1", "rgA", "app1", "logs", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("set blob: %v", err)
	}

	if _, err := m.SetPublishingPolicy(ctx, "sub1", "rgA", "app1", "scm", false); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	want, _ := m.GetSiteMeta(ctx, "sub1", "rgA", "app1")

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMetaMock()
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.GetSiteMeta(ctx, "sub1", "rgA", "app1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if string(got.SiteConfig) != string(want.SiteConfig) || string(got.Configs["logs"]) != `{"a":1}` ||
		got.PublishingPolicies["scm"] || got.PublishingPassword != want.PublishingPassword {
		t.Fatalf("restored = %+v, want %+v", got, want)
	}
}

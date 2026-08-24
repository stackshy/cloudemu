package acr_test

import (
	"context"
	"testing"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

func TestSDKACRDeleteTag(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")
	seedImage(t, reg, "app", "v2")

	if _, err := client.DeleteTag(ctx, "app", "v1", nil); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	var tags []string

	pager := client.NewListTagsPager("app", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListTags: %v", err)
		}

		for _, tag := range page.Tags {
			tags = append(tags, *tag.Name)
		}
	}

	if contains(tags, "v1") {
		t.Fatalf("tag v1 should be gone, tags=%v", tags)
	}

	if !contains(tags, "v2") {
		t.Fatalf("tag v2 should survive, tags=%v", tags)
	}
}

func TestSDKACRGetTagProperties(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")

	props, err := client.GetTagProperties(ctx, "app", "v1", nil)
	if err != nil {
		t.Fatalf("GetTagProperties: %v", err)
	}

	if props.Tag == nil || props.Tag.Name == nil || *props.Tag.Name != "v1" {
		t.Fatalf("got tag %v, want v1", props.Tag)
	}

	if props.Tag.Digest == nil || *props.Tag.Digest == "" {
		t.Fatal("expected tag digest populated")
	}

	if props.Tag.ChangeableAttributes == nil || props.Tag.ChangeableAttributes.CanDelete == nil ||
		!*props.Tag.ChangeableAttributes.CanDelete {
		t.Fatal("expected changeable attributes with deleteEnabled true")
	}

	if _, err := client.GetTagProperties(ctx, "app", "ghost", nil); err == nil {
		t.Fatal("expected error for unknown tag")
	}
}

func TestSDKACRListAndGetManifests(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")

	var digest string

	pager := client.NewListManifestsPager("app", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListManifests: %v", err)
		}

		for _, m := range page.Manifests.Attributes {
			if m.Digest != nil {
				digest = *m.Digest
			}
		}
	}

	if digest == "" {
		t.Fatal("expected at least one manifest with a digest")
	}

	got, err := client.GetManifestProperties(ctx, "app", digest, nil)
	if err != nil {
		t.Fatalf("GetManifestProperties: %v", err)
	}

	if got.Manifest == nil || got.Manifest.Digest == nil || *got.Manifest.Digest != digest {
		t.Fatalf("got manifest digest %v, want %s", got.Manifest, digest)
	}
}

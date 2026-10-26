package codeartifact_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/codeartifact"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

type publishedEvent struct {
	source, detailType string
	resources          []string
	detail             map[string]any
}

type recordingPublisher struct {
	mu     sync.Mutex
	events []publishedEvent
	// onPublish runs inside PublishServiceEvent to prove delivery happens
	// without the mock's lock held.
	onPublish func()
}

func (r *recordingPublisher) PublishServiceEvent(_ context.Context, source, detailType string, detail any, resources []string) {
	raw, _ := json.Marshal(detail)

	var parsed map[string]any

	_ = json.Unmarshal(raw, &parsed)

	r.mu.Lock()
	r.events = append(r.events, publishedEvent{source: source, detailType: detailType, resources: resources, detail: parsed})
	r.mu.Unlock()

	if r.onPublish != nil {
		r.onPublish()
	}
}

func (r *recordingPublisher) all() []publishedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]publishedEvent(nil), r.events...)
}

func wired(t *testing.T, repos ...string) (*codeartifact.Mock, *recordingPublisher) {
	t.Helper()

	m := seeded(t, config.NewOptions(), repos...)
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)

	return m, pub
}

func TestPackageVersionEventShape(t *testing.T) {
	opts := config.NewOptions()
	m, pub := wired(t, "repo")

	publishAsset(t, m, pkgRef("repo", "pkg"), "1.0.0", "f", []byte("x"), false)

	evs := pub.all()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}

	e := evs[0]
	if e.source != "aws.codeartifact" || e.detailType != "CodeArtifact Package Version State Change" {
		t.Fatalf("identity = %q / %q", e.source, e.detailType)
	}

	wantRes := "arn:aws:codeartifact:" + opts.Region + ":" + opts.AccountID + ":package/dom/repo/generic/ns/pkg"
	if len(e.resources) != 1 || e.resources[0] != wantRes {
		t.Fatalf("resources = %v, want [%s]", e.resources, wantRes)
	}

	want := map[string]any{
		"domainName": "dom", "domainOwner": opts.AccountID, "repositoryName": "repo", "repositoryAdministrator": opts.AccountID,
		"packageFormat": "generic", "packageNamespace": "ns", "packageName": "pkg", "packageVersion": "1.0.0",
		"packageVersionState": "Published", "operationType": "Created", "sequenceNumber": float64(1),
	}

	for k, v := range want {
		if e.detail[k] != v {
			t.Errorf("detail[%s] = %v, want %v", k, e.detail[k], v)
		}
	}

	if rev, _ := e.detail["packageVersionRevision"].(string); len(rev) != 32 {
		t.Errorf("revision = %v", e.detail["packageVersionRevision"])
	}

	if id, _ := e.detail["eventDeduplicationId"].(string); id == "" {
		t.Error("eventDeduplicationId empty")
	}

	changes, _ := e.detail["changes"].(map[string]any)
	if changes["assetsAdded"] != float64(1) || changes["assetsRemoved"] != float64(0) || changes["assetsUpdated"] != float64(0) ||
		changes["statusChanged"] != true || changes["metadataUpdated"] != false {
		t.Errorf("changes = %v", changes)
	}
}

func TestPackageVersionEventsAllMutations(t *testing.T) {
	m, pub := wired(t, "src", "dst")
	ctx := context.Background()
	ref := pkgRef("src", "pkg")

	publishAsset(t, m, ref, "1", "a", []byte("a"), true)  // Created, Unfinished
	publishAsset(t, m, ref, "1", "b", []byte("b"), false) // Updated, Published
	publishAsset(t, m, ref, "2", "a", []byte("a2"), false)

	_, err := m.UpdatePackageVersionsStatus(ctx, &driver.UpdateStatusInput{
		VersionsInput: driver.VersionsInput{Ref: ref, Versions: []string{"1"}}, TargetStatus: "Archived",
	})
	requireNoError(t, err)

	_, err = m.CopyPackageVersions(ctx, &driver.CopyInput{Ref: ref, SourceRepository: "src", DestinationRepository: "dst", Versions: []string{"2"}})
	requireNoError(t, err)

	_, err = m.DisposePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"2"}})
	requireNoError(t, err)

	_, err = m.DeletePackageVersions(ctx, &driver.VersionsInput{Ref: ref, Versions: []string{"1"}})
	requireNoError(t, err)

	type step struct {
		repo, version, op, state string
	}

	var got []step

	seq := map[string]float64{}

	for _, e := range pub.all() {
		got = append(got, step{e.detail["repositoryName"].(string), e.detail["packageVersion"].(string), e.detail["operationType"].(string), e.detail["packageVersionState"].(string)})

		key := e.detail["repositoryName"].(string)
		if s := e.detail["sequenceNumber"].(float64); s <= seq[key] {
			t.Errorf("sequenceNumber %v not increasing after %v in %s", s, seq[key], key)
		} else {
			seq[key] = s
		}
	}

	want := []step{
		{"src", "1", "Created", "Unfinished"}, {"src", "1", "Updated", "Published"}, {"src", "2", "Created", "Published"},
		{"src", "1", "Updated", "Archived"}, {"dst", "2", "Created", "Published"}, {"src", "2", "Updated", "Disposed"},
		{"src", "1", "Deleted", "Archived"},
	}

	if len(got) != len(want) {
		t.Fatalf("events = %+v", got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	ids := map[string]bool{}

	for _, e := range pub.all() {
		id := e.detail["eventDeduplicationId"].(string)
		if ids[id] {
			t.Errorf("duplicate eventDeduplicationId %q", id)
		}

		ids[id] = true
	}
}

func TestDeletePackageEmitsOneEventPerVersion(t *testing.T) {
	m, pub := wired(t, "repo")
	ref := pkgRef("repo", "pkg")

	publishAsset(t, m, ref, "1", "f", []byte("1"), false)
	publishAsset(t, m, ref, "2", "f", []byte("2"), false)

	before := len(pub.all())

	_, err := m.DeletePackage(context.Background(), ref)
	requireNoError(t, err)

	evs := pub.all()[before:]
	if len(evs) != 2 || evs[0].detail["operationType"] != "Deleted" || evs[1].detail["operationType"] != "Deleted" {
		t.Fatalf("delete-package events = %+v", evs)
	}
}

func TestEventsSingleEventWhenOwnerIsAdministrator(t *testing.T) {
	m, pub := wired(t, "repo")

	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)

	if n := len(pub.all()); n != 1 {
		t.Fatalf("events = %d, want exactly 1 (the domain owner is the repository administrator)", n)
	}
}

func TestEventsUnwiredAndRejectedAreNoOps(t *testing.T) {
	m := seeded(t, config.NewOptions(), "repo")
	publishAsset(t, m, pkgRef("repo", "pkg"), "1", "f", []byte("x"), false)

	m2, pub := wired(t, "repo")

	_, err := m2.PublishPackageVersion(context.Background(), &driver.PublishInput{
		Ref: pkgRef("repo", "pkg"), Version: "1", AssetName: "f", SHA256: sha([]byte("other")), Content: []byte("x"),
	})
	requireAPIError(t, err, driver.ExValidation)

	if n := len(pub.all()); n != 0 {
		t.Fatalf("rejected publish emitted %d events", n)
	}

	_, err = m2.UpdatePackageVersionsStatus(context.Background(), &driver.UpdateStatusInput{
		VersionsInput: driver.VersionsInput{Ref: pkgRef("repo", "pkg"), Versions: []string{"nope"}}, TargetStatus: "Archived",
	})
	requireAPIError(t, err, driver.ExNotFound)

	if n := len(pub.all()); n != 0 {
		t.Fatalf("failed status update emitted %d events", n)
	}
}

func TestEventDeliveryCanCallBackWithoutDeadlock(t *testing.T) {
	m, pub := wired(t, "repo")
	ref := pkgRef("repo", "pkg")

	pub.onPublish = func() {
		// A rule target calling back into CodeArtifact while an event is delivered.
		if _, err := m.DescribeDomain(context.Background(), "dom", ""); err != nil {
			t.Errorf("callback: %v", err)
		}
	}

	publishAsset(t, m, ref, "1", "f", []byte("x"), false)
}

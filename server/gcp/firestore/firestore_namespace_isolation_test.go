// These tests assert Firestore's per-(project, database) namespace isolation:
// a document written under one project/database must never be visible under a
// different project or a different (default vs named) database, even when the
// collection path and document id are identical. Cross-project isolation is
// exercised with the REAL cloud.google.com/go/firestore REST SDK (two clients,
// distinct project ids, one server). Cross-database isolation is exercised over
// raw REST — the REST SDK constructor hard-codes the "(default)" database, so
// named databases are only reachable at the wire level.
package firestore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newIsolationServer boots a fresh emulator + GCP server shared by every client
// in a test. Collections are created lazily by the handler on first write, so
// nothing is pre-declared here.
func newIsolationServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func newRESTClientForProject(t *testing.T, ts *httptest.Server, project string) *gcpfirestore.Client {
	t.Helper()

	client, err := gcpfirestore.NewRESTClient(context.Background(), project,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient(%s): %v", project, err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestCrossProjectDocumentIsolation writes the same collection/doc under two
// different projects through the real SDK and asserts each project sees only
// its own document — the historical bug collapsed both projects into one
// collection namespace, so project B could read (and clobber) project A's data.
func TestCrossProjectDocumentIsolation(t *testing.T) {
	ts := newIsolationServer(t)
	ctx := context.Background()

	clientA := newRESTClientForProject(t, ts, "project-a")
	clientB := newRESTClientForProject(t, ts, "project-b")

	// Both write cities/SF, with distinct payloads.
	if _, err := clientA.Collection("cities").Doc("SF").Set(ctx, map[string]any{"owner": "A"}); err != nil {
		t.Fatalf("A Set: %v", err)
	}

	if _, err := clientB.Collection("cities").Doc("SF").Set(ctx, map[string]any{"owner": "B"}); err != nil {
		t.Fatalf("B Set: %v", err)
	}

	// Each project must read back its OWN value, never the other's.
	snapA, err := clientA.Collection("cities").Doc("SF").Get(ctx)
	if err != nil {
		t.Fatalf("A Get: %v", err)
	}

	if got := snapA.Data()["owner"]; got != "A" {
		t.Errorf("project A sees owner=%v, want A (cross-project bleed)", got)
	}

	snapB, err := clientB.Collection("cities").Doc("SF").Get(ctx)
	if err != nil {
		t.Fatalf("B Get: %v", err)
	}

	if got := snapB.Data()["owner"]; got != "B" {
		t.Errorf("project B sees owner=%v, want B (cross-project bleed)", got)
	}

	// A document that exists ONLY under project A must be absent under project B.
	if _, err := clientA.Collection("secrets").Doc("only-a").Set(ctx, map[string]any{"v": 1}); err != nil {
		t.Fatalf("A Set secret: %v", err)
	}

	if _, err := clientB.Collection("secrets").Doc("only-a").Get(ctx); err == nil {
		t.Fatal("project B could read a document written only under project A (cross-project leak)")
	}

	// Listing project B's cities must not surface project A's exclusive docs.
	if _, err := clientA.Collection("cities").Doc("LA").Set(ctx, map[string]any{"owner": "A"}); err != nil {
		t.Fatalf("A Set LA: %v", err)
	}

	seenB := listDocIDs(t, ctx, clientB.Collection("cities"))
	if seenB["LA"] {
		t.Errorf("project B list saw LA, which belongs only to project A: %v", seenB)
	}

	if !seenB["SF"] {
		t.Errorf("project B list missing its own SF: %v", seenB)
	}
}

func listDocIDs(t *testing.T, ctx context.Context, coll *gcpfirestore.CollectionRef) map[string]bool {
	t.Helper()

	seen := map[string]bool{}
	it := coll.Documents(ctx)

	for {
		s, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("list iterator: %v", err)
		}

		seen[s.Ref.ID] = true
	}

	return seen
}

// TestCrossDatabaseDocumentIsolation writes the same collection/doc under the
// default database and a named database of the SAME project, over raw REST, and
// asserts each database sees only its own document. The historical bug ignored
// the database id in the storage key, collapsing "(default)" and named
// databases into one namespace.
func TestCrossDatabaseDocumentIsolation(t *testing.T) {
	ts := newIsolationServer(t)

	const project = "shared-project"

	// Write cities/SF with distinct payloads under two databases.
	restCreate(t, ts, project, "(default)", "cities", "SF", map[string]any{"db": "default"})
	restCreate(t, ts, project, "named1", "cities", "SF", map[string]any{"db": "named1"})

	// Each database reads back its OWN value.
	if got := restGetField(t, ts, project, "(default)", "cities", "SF"); got != "default" {
		t.Errorf("(default) db sees db=%v, want default (cross-database bleed)", got)
	}

	if got := restGetField(t, ts, project, "named1", "cities", "SF"); got != "named1" {
		t.Errorf("named1 db sees db=%v, want named1 (cross-database bleed)", got)
	}

	// A doc written only under named1 must be absent under (default).
	restCreate(t, ts, project, "named1", "secrets", "only-named", map[string]any{"v": "x"})

	if status := restGetStatus(t, ts, project, "(default)", "secrets", "only-named"); status != http.StatusNotFound {
		t.Errorf("(default) db read of a named1-only document returned %d, want 404 (cross-database leak)", status)
	}
}

// restBaseURL builds the documents-root URL for a project/database.
func restBaseURL(ts *httptest.Server, project, database, coll, doc string) string {
	return ts.URL + "/v1/projects/" + project + "/databases/" + database + "/documents/" + coll + "/" + doc
}

func restCreate(t *testing.T, ts *httptest.Server, project, database, coll, doc string, fields map[string]any) {
	t.Helper()

	body := map[string]any{"fields": map[string]any{}}
	for k, v := range fields {
		s, _ := v.(string)
		body["fields"].(map[string]any)[k] = map[string]any{"stringValue": s}
	}

	buf, _ := json.Marshal(body)

	url := ts.URL + "/v1/projects/" + project + "/databases/" + database + "/documents/" + coll + "?documentId=" + doc

	resp, err := ts.Client().Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("create %s/%s/%s/%s: %v", project, database, coll, doc, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("create %s/%s/%s/%s: status %d: %s", project, database, coll, doc, resp.StatusCode, out)
	}
}

func restGetStatus(t *testing.T, ts *httptest.Server, project, database, coll, doc string) int {
	t.Helper()

	resp, err := ts.Client().Get(restBaseURL(ts, project, database, coll, doc))
	if err != nil {
		t.Fatalf("get %s/%s/%s/%s: %v", project, database, coll, doc, err)
	}

	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode
}

func restGetField(t *testing.T, ts *httptest.Server, project, database, coll, doc string) string {
	t.Helper()

	resp, err := ts.Client().Get(restBaseURL(ts, project, database, coll, doc))
	if err != nil {
		t.Fatalf("get %s/%s/%s/%s: %v", project, database, coll, doc, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("get %s/%s/%s/%s: status %d: %s", project, database, coll, doc, resp.StatusCode, out)
	}

	var decoded struct {
		Fields map[string]struct {
			StringValue string `json:"stringValue"`
		} `json:"fields"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode get %s/%s/%s/%s: %v", project, database, coll, doc, err)
	}

	return decoded.Fields["db"].StringValue
}

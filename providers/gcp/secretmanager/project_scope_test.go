package secretmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func names(t *testing.T, m *Mock, ctx context.Context) []string {
	t.Helper()

	list, err := m.ListSecrets(ctx)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	out := make([]string, 0, len(list))
	for i := range list {
		out = append(out, list[i].ResourceID)
	}

	return out
}

func TestProjectScopedSecrets(t *testing.T) {
	m := newTestMock()
	def := m.opts.ProjectID
	bg := context.Background()
	pb := projectctx.WithProject(bg, "p-b")

	if _, err := m.CreateSecret(bg, driver.SecretConfig{Name: "sec1"}, []byte("a")); err != nil {
		t.Fatalf("create default: %v", err)
	}

	if _, err := m.CreateSecret(pb, driver.SecretConfig{Name: "sec1"}, []byte("b")); err != nil {
		t.Fatalf("create p-b with the same id: %v", err)
	}

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"unstamped is the default project", bg, "a"},
		{"stamped default project", projectctx.WithProject(bg, def), "a"},
		{"other project", pb, "b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := m.GetSecretValue(tc.ctx, "sec1", "")
			if err != nil || string(v.Value) != tc.want {
				t.Fatalf("GetSecretValue = %v, %v; want %q", v, err, tc.want)
			}

			if got := names(t, m, tc.ctx); len(got) != 1 {
				t.Fatalf("ListSecrets = %v, want one secret", got)
			}
		})
	}

	if got := names(t, m, projectctx.AllProjects(bg)); len(got) != 2 {
		t.Fatalf("AllProjects ListSecrets = %v, want both", got)
	}

	if err := m.DeleteSecret(pb, "sec1"); err != nil {
		t.Fatalf("delete p-b: %v", err)
	}

	if _, err := m.GetSecret(bg, "sec1"); err != nil {
		t.Fatalf("default secret after p-b delete: %v", err)
	}
}

func TestProjectScopedSecretsConcurrent(t *testing.T) {
	m := newTestMock()

	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			ctx := projectctx.WithProject(context.Background(), fmt.Sprintf("p-%d", i%2))
			_, _ = m.CreateSecret(ctx, driver.SecretConfig{Name: fmt.Sprintf("s%d", i/2)}, []byte("v"))
			_, _ = m.ListSecrets(ctx)
		}(i)
	}

	wg.Wait()

	if got := names(t, m, projectctx.AllProjects(context.Background())); len(got) != 20 {
		t.Fatalf("got %d secrets, want 20", len(got))
	}
}

// TestRestoreAdoptsLegacySecrets loads a snapshot taken before project
// scoping (bare secret ids as keys) and checks the records land in the
// default project, with one warning.
func TestRestoreAdoptsLegacySecrets(t *testing.T) {
	var buf bytes.Buffer

	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)

	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	legacy := json.RawMessage(`{"secrets":{
		"sec1":{"info":{"Name":"sec1"},"versions":[{"VersionID":"1","Value":"YQ==","Current":true,"State":"ENABLED"}],"verCounter":1},
		"sec2":{"info":{"Name":"sec2"}}}}`)

	m := newTestMock()
	bg := context.Background()

	if err := m.Restore(bg, legacy); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if v, err := m.GetSecretValue(bg, "sec1", ""); err != nil || string(v.Value) != "a" {
		t.Fatalf("default-project GetSecretValue = %v, %v", v, err)
	}

	if _, err := m.GetSecret(projectctx.WithProject(bg, "p-b"), "sec1"); err == nil {
		t.Fatal("legacy secret visible under another project")
	}

	out := buf.String()
	if strings.Count(out, "gcp/secretmanager: adopted 2 legacy") != 1 || !strings.Contains(out, m.opts.ProjectID) {
		t.Fatalf("restore log = %q, want one adopted-2 warning", out)
	}

	first, err := m.Snapshot(bg, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	again := newTestMock()
	if err := again.Restore(bg, first); err != nil {
		t.Fatalf("second Restore: %v", err)
	}

	second, err := again.Snapshot(bg, false)
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("round trip changed the snapshot:\n%s\n%s", first, second)
	}
}

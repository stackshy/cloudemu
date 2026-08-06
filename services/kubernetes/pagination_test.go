// Tests for the k8s chunked-list pager (PR #314, Finding 6): key-anchored
// continue tokens (no skip/duplicate under concurrent mutation) and a 410 Gone
// on a malformed token, replacing the old integer-offset behavior.

package kubernetes_test

import (
	"fmt"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// seedConfigMaps creates n config maps named cm-00, cm-01, … in the default
// namespace so their sort key (default/cm-NN) matches numeric order.
func seedConfigMaps(t *testing.T, base string, n int) {
	t.Helper()

	for i := 0; i < n; i++ {
		createConfigMap(t, base, fmt.Sprintf("cm-%02d", i))
	}
}

func createConfigMap(t *testing.T, base, name string) {
	t.Helper()

	cm := mustJSON(t, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       map[string]string{"k": "v"},
	})
	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/configmaps", cm)
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create configmap %s: got %d, want 201", name, resp.StatusCode)
	}
}

func deleteConfigMap(t *testing.T, base, name string) {
	t.Helper()

	resp := do(t, http.MethodDelete, base+"/api/v1/namespaces/default/configmaps/"+name, nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete configmap %s: got %d, want 200", name, resp.StatusCode)
	}
}

// listConfigMapPage fetches one page and returns the item names plus the
// continue token. cont is the raw ?continue= value ("" for the first page).
func listConfigMapPage(t *testing.T, base string, limit int, cont string) ([]string, string) {
	t.Helper()

	url := fmt.Sprintf("%s/api/v1/namespaces/default/configmaps?limit=%d", base, limit)
	if cont != "" {
		url += "&continue=" + cont
	}

	resp := do(t, http.MethodGet, url, nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("list page (continue=%q): got %d, want 200", cont, resp.StatusCode)
	}

	var list corev1.ConfigMapList
	mustDecode(t, resp.Body, &list)

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}

	return names, list.Continue
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}

	return false
}

// TestPager_NoSkipOnDeleteBeforeBoundary is the core Finding-6 regression: with
// the old integer offset, deleting an item before the page boundary shifted the
// offset and skipped the item that slid into that slot. Key-anchored resume
// makes page 2 start strictly after the boundary key regardless.
func TestPager_NoSkipOnDeleteBeforeBoundary(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	seedConfigMaps(t, base, 10) // cm-00 .. cm-09

	page1, cont := listConfigMapPage(t, base, 3, "")
	if want := []string{"cm-00", "cm-01", "cm-02"}; !equalStrings(page1, want) {
		t.Fatalf("page1: got %v, want %v", page1, want)
	}

	if cont == "" {
		t.Fatal("page1: expected a non-empty continue token")
	}

	// Delete cm-01, which sorts BEFORE the boundary key cm-02.
	deleteConfigMap(t, base, "cm-01")

	page2, _ := listConfigMapPage(t, base, 3, cont)

	// Resume is anchored to cm-02, so page2 begins at cm-03 — no skip of cm-03
	// (the old offset bug) and no duplicate of cm-02.
	if want := []string{"cm-03", "cm-04", "cm-05"}; !equalStrings(page2, want) {
		t.Fatalf("page2 after delete-before-boundary: got %v, want %v", page2, want)
	}

	if contains(page2, "cm-02") {
		t.Fatal("page2 duplicated the boundary item cm-02")
	}
}

// TestPager_DeleteAfterBoundaryAbsent verifies an item deleted after the
// boundary is simply not served (correct), and an item inserted before the
// boundary does not perturb the page-2 window.
func TestPager_DeleteAfterBoundaryAbsent(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	seedConfigMaps(t, base, 10) // cm-00 .. cm-09

	_, cont := listConfigMapPage(t, base, 3, "") // boundary key = cm-02

	// Delete cm-05 (after the boundary) and insert cm-015 (before it).
	deleteConfigMap(t, base, "cm-05")
	createConfigMap(t, base, "cm-015")

	page2, _ := listConfigMapPage(t, base, 3, cont)

	if contains(page2, "cm-05") {
		t.Fatalf("page2 served the deleted-after-boundary item cm-05: %v", page2)
	}

	if contains(page2, "cm-015") {
		t.Fatalf("page2 window shifted to include the before-boundary insert cm-015: %v", page2)
	}

	// cm-05 is gone, so the next three after cm-02 are cm-03, cm-04, cm-06.
	if want := []string{"cm-03", "cm-04", "cm-06"}; !equalStrings(page2, want) {
		t.Fatalf("page2: got %v, want %v", page2, want)
	}
}

// TestPager_MalformedTokenGone asserts a garbage continue value returns 410 Gone
// with a Status body (client-go's ResourceExpired contract), not a 200 full list.
func TestPager_MalformedTokenGone(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	seedConfigMaps(t, base, 5)

	url := base + "/api/v1/namespaces/default/configmaps?limit=2&continue=@@not-base64@@"
	resp := do(t, http.MethodGet, url, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusGone {
		t.Fatalf("malformed continue token: got %d, want 410", resp.StatusCode)
	}

	var status metav1.Status
	mustDecode(t, resp.Body, &status)

	if status.Kind != "Status" || status.Reason != metav1.StatusReasonExpired {
		t.Fatalf("malformed token body: got kind=%q reason=%q, want Status/Expired", status.Kind, status.Reason)
	}
}

// TestPager_RoundTripCoversAllOnce pages through every object in chunks and
// asserts each appears exactly once and the final page has an empty continue.
func TestPager_RoundTripCoversAllOnce(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	const total = 10

	seedConfigMaps(t, base, total)

	seen := map[string]int{}
	cont := ""
	pages := 0

	for {
		names, next := listConfigMapPage(t, base, 3, cont)
		for _, n := range names {
			seen[n]++
		}

		pages++
		if pages > total+1 {
			t.Fatal("pager did not terminate")
		}

		if next == "" {
			break
		}

		cont = next
	}

	if len(seen) != total {
		t.Fatalf("distinct objects seen: got %d, want %d (%v)", len(seen), total, seen)
	}

	for name, count := range seen {
		if count != 1 {
			t.Fatalf("object %s served %d times, want exactly 1", name, count)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

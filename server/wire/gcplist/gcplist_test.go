package gcplist

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestCompute(t *testing.T) {
	tests := []struct {
		query   string
		want    int
		wantErr bool
	}{
		{"", ComputeMaxResults, false},
		{"maxResults=0", ComputeMaxResults, false},
		{"maxResults=2", 2, false},
		{"maxResults=500", 500, false},
		{"maxResults=501", 0, true},
		{"maxResults=-1", 0, true},
		{"maxResults=abc", 0, true},
	}

	for _, tc := range tests {
		q, _ := url.ParseQuery(tc.query)

		p, err := Compute(q)
		if tc.wantErr {
			if !errors.Is(err, ErrInvalidMaxResults) {
				t.Errorf("%q: err = %v, want ErrInvalidMaxResults", tc.query, err)
			}

			continue
		}

		if err != nil || p.Size != tc.want {
			t.Errorf("%q: size %d err %v, want %d", tc.query, p.Size, err, tc.want)
		}
	}
}

func TestPageWalk(t *testing.T) {
	items := []string{"e", "c", "a", "d", "b"}
	key := func(s string) string { return s }

	var (
		got   []string
		sizes []int
		token string
	)

	for {
		page, next, err := Page(append([]string(nil), items...), key, Params{Size: 2, Token: token})
		if err != nil {
			t.Fatalf("Page: %v", err)
		}

		got = append(got, page...)
		sizes = append(sizes, len(page))

		if next == "" {
			break
		}

		token = next
	}

	if strings.Join(got, "") != "abcde" {
		t.Errorf("walk = %v, want a..e once each in order", got)
	}

	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 {
		t.Errorf("page sizes = %v, want [2 2 1]", sizes)
	}

	if _, _, err := Page(items, key, Params{Size: 2, Token: "bogus!"}); !errors.Is(err, ErrInvalidPageToken) {
		t.Errorf("malformed token: err = %v, want ErrInvalidPageToken", err)
	}

	empty, next, err := Page([]string{}, key, Params{Size: 2})
	if err != nil || empty == nil || next != "" {
		t.Errorf("empty list: page %#v next %q err %v, want non-nil empty page", empty, next, err)
	}
}

// TestPageSurvivesChangesBetweenPages pins the keyset cursor: an insert
// before the cursor does not repeat an item, and deleting the items around
// the cursor neither repeats nor rejects the token.
func TestPageSurvivesChangesBetweenPages(t *testing.T) {
	key := func(s string) string { return s }

	first, token, err := Page([]string{"b", "d", "f", "h", "j"}, key, Params{Size: 2})
	if err != nil || strings.Join(first, "") != "bd" || token == "" {
		t.Fatalf("page 1 = %v %q %v", first, token, err)
	}

	tests := []struct {
		name  string
		items []string
		want  string
	}{
		{"insert before cursor", []string{"a", "b", "d", "f", "h", "j"}, "fh"},
		{"insert after cursor", []string{"b", "d", "e", "f", "h", "j"}, "ef"},
		{"cursor item deleted", []string{"b", "f", "h", "j"}, "fh"},
		{"everything after deleted", []string{"b"}, ""},
	}

	for _, tc := range tests {
		page, _, err := Page(tc.items, key, Params{Size: 2, Token: token})
		if err != nil || strings.Join(page, "") != tc.want {
			t.Errorf("%s: page %v err %v, want %q", tc.name, page, err, tc.want)
		}
	}
}

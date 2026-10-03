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

	_, stale, _ := Page(append([]string(nil), items...), key, Params{Size: 4})

	for _, tok := range []string{"bogus", stale} {
		if _, _, err := Page(items[:2], key, Params{Size: 2, Token: tok}); !errors.Is(err, ErrInvalidPageToken) {
			t.Errorf("token %q: err = %v, want ErrInvalidPageToken", tok, err)
		}
	}
}

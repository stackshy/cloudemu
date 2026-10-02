package azurearm_test

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func TestPaginate(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}

	cases := map[string]struct {
		url      string
		tls      bool
		size     int
		want     []int
		wantNext string
	}{
		"single page":      {"/x?api-version=1", false, 10, []int{0, 1, 2, 3, 4}, ""},
		"first page":       {"/x?api-version=1", false, 2, []int{0, 1}, "http://h/x?%24skip=2&api-version=1"},
		"middle page":      {"/x?api-version=1&$skip=2", true, 2, []int{2, 3}, "https://h/x?%24skip=4&api-version=1"},
		"top narrows page": {"/x?$top=1", false, 100, []int{0}, "http://h/x?%24skip=1&%24top=1"},
		"last page":        {"/x?$skip=4", false, 2, []int{4}, ""},
		"past the end":     {"/x?$skip=9", false, 2, []int{}, ""},
		"bad skip ignored": {"/x?$skip=-3&$top=zz", false, 10, []int{0, 1, 2, 3, 4}, ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.url, nil)
			r.Host = "h"

			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}

			page, next := azurearm.Paginate(r, items, tc.size)
			if len(page) != len(tc.want) || next != tc.wantNext {
				t.Fatalf("page=%v next=%q, want %v %q", page, next, tc.want, tc.wantNext)
			}

			for i := range page {
				if page[i] != tc.want[i] {
					t.Fatalf("page=%v, want %v", page, tc.want)
				}
			}
		})
	}
}

package ec2

import (
	"encoding/base64"
	"strconv"
	"testing"
)

// idOfString is the identity id-selector used by the paginateXML unit tests.
func idOfString(s string) string { return s }

func pageIDs(items []string) []string { return items }

// TestPaginateXMLFullSetWhenNoLimit pins that an empty, zero, or unparsable
// MaxResults returns the whole set with no NextToken (DescribeInstances parity).
func TestPaginateXMLFullSetWhenNoLimit(t *testing.T) {
	items := []string{"a", "b", "c"}

	for _, max := range []string{"", "0", "-1", "abc"} {
		page, next := paginateXML(items, max, "", idOfString)
		if len(page) != 3 || next != "" {
			t.Fatalf("MaxResults=%q: page=%v next=%q, want full set and empty token", max, page, next)
		}
	}
}

// TestPaginateXMLPageAndToken pins that a limit smaller than the set returns
// exactly that many items plus a NextToken pointing at the next item's id.
func TestPaginateXMLPageAndToken(t *testing.T) {
	items := []string{"a", "b", "c"}

	page, next := paginateXML(items, "2", "", idOfString)
	if len(page) != 2 || page[0] != "a" || page[1] != "b" {
		t.Fatalf("page = %v, want [a b]", page)
	}

	wantToken := base64.StdEncoding.EncodeToString([]byte("c"))
	if next != wantToken {
		t.Fatalf("next = %q, want %q (base64 of the first item on the next page)", next, wantToken)
	}
}

// TestPaginateXMLRoundTripResumesAndTerminates pins that following the emitted
// NextToken resumes at the correct offset and the final page carries no token.
func TestPaginateXMLRoundTripResumesAndTerminates(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}

	var seen []string

	token := ""
	for range items {
		page, next := paginateXML(items, "2", token, idOfString)
		seen = append(seen, pageIDs(page)...)

		if next == "" {
			break
		}

		token = next
	}

	if got := len(seen); got != len(items) {
		t.Fatalf("paged through %d items, want %d (no dupes/gaps)", got, len(items))
	}

	for i := range items {
		if seen[i] != items[i] {
			t.Fatalf("page-through order = %v, want %v", seen, items)
		}
	}
}

// TestPaginateXMLBoundaryLimitEqualsLen pins that a limit equal to the set size
// returns everything with no NextToken (there is no next page).
func TestPaginateXMLBoundaryLimitEqualsLen(t *testing.T) {
	items := []string{"a", "b", "c"}

	page, next := paginateXML(items, "3", "", idOfString)
	if len(page) != 3 || next != "" {
		t.Fatalf("limit==len: page=%v next=%q, want full set and empty token", page, next)
	}
}

// TestPaginateXMLUnknownTokenStartsAtZero pins that a garbage or non-base64
// NextToken restarts from the beginning rather than erroring or skipping.
func TestPaginateXMLUnknownTokenStartsAtZero(t *testing.T) {
	items := []string{"a", "b", "c"}

	for _, tok := range []string{"!!!not-base64!!!", base64.StdEncoding.EncodeToString([]byte("zzz"))} {
		page, _ := paginateXML(items, "1", tok, idOfString)
		if len(page) != 1 || page[0] != "a" {
			t.Fatalf("token=%q: page=%v, want to restart at [a]", tok, page)
		}
	}
}

// TestPaginateXMLCapsAtMaxDescribeResults pins that a MaxResults above the 1000
// cap is clamped, so an over-large limit still emits a NextToken when more items
// remain rather than returning everything.
func TestPaginateXMLCapsAtMaxDescribeResults(t *testing.T) {
	items := make([]string, maxDescribeResults+1)
	for i := range items {
		// zero-padded so lexical order matches numeric order.
		items[i] = strconv.Itoa(1000000 + i)
	}

	page, next := paginateXML(items, strconv.Itoa(maxDescribeResults+500), "", idOfString)
	if len(page) != maxDescribeResults {
		t.Fatalf("page size = %d, want cap %d", len(page), maxDescribeResults)
	}

	if next == "" {
		t.Fatalf("next token empty, want a cursor to the %dth item", maxDescribeResults)
	}
}

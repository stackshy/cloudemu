package pagination

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeToken(t *testing.T) {
	tests := []struct {
		name   string
		offset int
	}{
		{name: "zero offset", offset: 0},
		{name: "small offset", offset: 5},
		{name: "large offset", offset: 10000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := EncodeToken(tc.offset)
			assert.NotEmpty(t, token)

			decoded, err := DecodeToken(token)
			require.NoError(t, err)
			assert.Equal(t, tc.offset, decoded.Offset)
		})
	}
}

func TestDecodeToken_Empty(t *testing.T) {
	decoded, err := DecodeToken("")
	require.NoError(t, err)
	assert.Equal(t, 0, decoded.Offset)
}

func TestDecodeToken_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "not base64", token: "!!!invalid!!!"},
		{name: "valid base64 but not json", token: "aGVsbG8="},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeToken(tc.token)
			assert.Error(t, err)
		})
	}
}

func TestPaginate(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f", "g"}

	tests := []struct {
		name          string
		pageToken     string
		maxResults    int
		expectItems   []string
		expectHasMore bool
		expectLen     int
	}{
		{
			name:          "first page of 3",
			pageToken:     "",
			maxResults:    3,
			expectItems:   []string{"a", "b", "c"},
			expectHasMore: true,
			expectLen:     3,
		},
		{
			name:          "default page size with empty token",
			pageToken:     "",
			maxResults:    0,
			expectItems:   items,
			expectHasMore: false,
			expectLen:     7,
		},
		{
			name:          "exact fit page",
			pageToken:     "",
			maxResults:    7,
			expectItems:   items,
			expectHasMore: false,
			expectLen:     7,
		},
		{
			name:          "page larger than items",
			pageToken:     "",
			maxResults:    100,
			expectItems:   items,
			expectHasMore: false,
			expectLen:     7,
		},
		{
			name:          "page size 1",
			pageToken:     "",
			maxResults:    1,
			expectItems:   []string{"a"},
			expectHasMore: true,
			expectLen:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := Paginate(items, tc.pageToken, tc.maxResults)
			require.NoError(t, err)
			assert.Equal(t, tc.expectItems, page.Items)
			assert.Equal(t, tc.expectHasMore, page.HasMore)
			assert.Len(t, page.Items, tc.expectLen)
		})
	}
}

func TestPaginate_MultiplePages(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	page1, err := Paginate(items, "", 2)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, page1.Items)
	assert.True(t, page1.HasMore)
	assert.NotEmpty(t, page1.NextPageToken)

	page2, err := Paginate(items, page1.NextPageToken, 2)
	require.NoError(t, err)
	assert.Equal(t, []int{3, 4}, page2.Items)
	assert.True(t, page2.HasMore)
	assert.NotEmpty(t, page2.NextPageToken)

	page3, err := Paginate(items, page2.NextPageToken, 2)
	require.NoError(t, err)
	assert.Equal(t, []int{5}, page3.Items)
	assert.False(t, page3.HasMore)
	assert.Empty(t, page3.NextPageToken)
}

func TestPaginate_EmptySlice(t *testing.T) {
	page, err := Paginate([]string{}, "", 10)
	require.NoError(t, err)
	assert.Nil(t, page.Items)
	assert.False(t, page.HasMore)
}

func TestPaginate_OffsetBeyondItems(t *testing.T) {
	items := []string{"a", "b"}
	token := EncodeToken(100)

	page, err := Paginate(items, token, 10)
	require.NoError(t, err)
	assert.Nil(t, page.Items)
	assert.False(t, page.HasMore)
}

func TestPaginate_InvalidToken(t *testing.T) {
	_, err := Paginate([]string{"a"}, "bad-token", 10)
	assert.Error(t, err)
}

func TestDecodeToken_NegativeOffset(t *testing.T) {
	// A well-formed token (valid base64 + valid JSON) carrying a negative
	// offset must be rejected as invalid, not returned for use as a bound.
	token := EncodeToken(-5)

	_, err := DecodeToken(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestPaginate_TokenInputsNeverPanic(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}

	negativeToken := EncodeToken(-5)
	// Hand-craft a base64+JSON token with an explicit negative offset, not via
	// EncodeToken, to mirror a crafted/corrupted client token.
	craftedNegative := base64.StdEncoding.EncodeToString([]byte(`{"offset":-1}`))
	beyondLenToken := EncodeToken(100)
	midListToken := EncodeToken(2)

	tests := []struct {
		name        string
		token       string
		wantErr     error
		wantItems   []string
		wantHasMore bool
	}{
		{
			name:      "negative offset well-formed token is invalid",
			token:     negativeToken,
			wantErr:   ErrInvalidToken,
			wantItems: nil,
		},
		{
			name:      "crafted negative offset token is invalid",
			token:     craftedNegative,
			wantErr:   ErrInvalidToken,
			wantItems: nil,
		},
		{
			name:      "offset beyond len yields empty page no error",
			token:     beyondLenToken,
			wantItems: nil,
		},
		{
			name:        "valid mid-list token returns correct page",
			token:       midListToken,
			wantItems:   []string{"c", "d"},
			wantHasMore: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				page, err := Paginate(items, tc.token, 2)
				if tc.wantErr != nil {
					require.Error(t, err)
					assert.ErrorIs(t, err, tc.wantErr)

					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.wantItems, page.Items)
				assert.Equal(t, tc.wantHasMore, page.HasMore)
			})
		})
	}
}

func TestPaginate_MalformedTokenBehaviorUnchanged(t *testing.T) {
	// The established contract for this helper: a non-base64 / non-JSON token
	// returns an error (not a silent offset-0 restart). Keep it intact.
	items := []string{"a", "b"}

	_, err := Paginate(items, "!!!not-base64!!!", 10)
	assert.Error(t, err)

	_, err = Paginate(items, "aGVsbG8=", 10) // valid base64, not JSON
	assert.Error(t, err)
}

func TestPaginateSorted_StableAcrossShuffledInput(t *testing.T) {
	// Same logical set presented in different orders must yield identical
	// pages — the invariant PaginateSorted exists to enforce.
	a := []string{"c", "a", "e", "b", "d"}
	b := []string{"e", "d", "c", "b", "a"}
	less := func(x, y string) bool { return x < y }

	p1, err := PaginateSorted(a, less, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := PaginateSorted(b, less, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Items[0] != p2.Items[0] || p1.Items[1] != p2.Items[1] {
		t.Fatalf("pages differ across input orders: %v vs %v", p1.Items, p2.Items)
	}

	p3, err := PaginateSorted(b, less, p2.NextPageToken, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Items[0] != "c" || p3.Items[1] != "d" {
		t.Fatalf("page 2 = %v, want [c d]", p3.Items)
	}
}

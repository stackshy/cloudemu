package pagination

import (
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

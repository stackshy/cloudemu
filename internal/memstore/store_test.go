package memstore

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_SetAndGet(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		expectOK  bool
		expectVal string
	}{
		{name: "set and get existing key", key: "k1", value: "v1", expectOK: true, expectVal: "v1"},
		{name: "get missing key", key: "missing", value: "", expectOK: false, expectVal: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[string]()

			switch tc.name {
			case "set and get existing key":
				s.Set(tc.key, tc.value)
			}

			val, ok := s.Get(tc.key)
			assert.Equal(t, tc.expectOK, ok)
			assert.Equal(t, tc.expectVal, val)
		})
	}
}

func TestStore_Has(t *testing.T) {
	tests := []struct {
		name   string
		setup  map[string]int
		key    string
		expect bool
	}{
		{name: "key exists", setup: map[string]int{"a": 1}, key: "a", expect: true},
		{name: "key does not exist", setup: map[string]int{"a": 1}, key: "b", expect: false},
		{name: "empty store", setup: map[string]int{}, key: "a", expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[int]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			assert.Equal(t, tc.expect, s.Has(tc.key))
		})
	}
}

func TestStore_Delete(t *testing.T) {
	tests := []struct {
		name       string
		setup      map[string]string
		deleteKey  string
		expectDel  bool
		expectLen  int
	}{
		{name: "delete existing key", setup: map[string]string{"a": "1", "b": "2"}, deleteKey: "a", expectDel: true, expectLen: 1},
		{name: "delete missing key", setup: map[string]string{"a": "1"}, deleteKey: "z", expectDel: false, expectLen: 1},
		{name: "delete from empty store", setup: map[string]string{}, deleteKey: "x", expectDel: false, expectLen: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[string]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			ok := s.Delete(tc.deleteKey)
			assert.Equal(t, tc.expectDel, ok)
			assert.Equal(t, tc.expectLen, s.Len())
		})
	}
}

func TestStore_Len(t *testing.T) {
	tests := []struct {
		name   string
		setup  map[string]int
		expect int
	}{
		{name: "empty store", setup: map[string]int{}, expect: 0},
		{name: "one item", setup: map[string]int{"a": 1}, expect: 1},
		{name: "multiple items", setup: map[string]int{"a": 1, "b": 2, "c": 3}, expect: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[int]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			assert.Equal(t, tc.expect, s.Len())
		})
	}
}

func TestStore_Keys(t *testing.T) {
	tests := []struct {
		name   string
		setup  map[string]int
		expect []string
	}{
		{name: "empty store", setup: map[string]int{}, expect: []string{}},
		{name: "single key", setup: map[string]int{"a": 1}, expect: []string{"a"}},
		{name: "multiple keys", setup: map[string]int{"b": 2, "a": 1, "c": 3}, expect: []string{"a", "b", "c"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[int]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			keys := s.Keys()
			sort.Strings(keys)
			assert.Equal(t, tc.expect, keys)
		})
	}
}

func TestStore_All(t *testing.T) {
	tests := []struct {
		name   string
		setup  map[string]string
		expect map[string]string
	}{
		{name: "empty store", setup: map[string]string{}, expect: map[string]string{}},
		{name: "with items", setup: map[string]string{"x": "1", "y": "2"}, expect: map[string]string{"x": "1", "y": "2"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[string]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			all := s.All()
			assert.Equal(t, tc.expect, all)
		})
	}
}

func TestStore_Update(t *testing.T) {
	tests := []struct {
		name      string
		setup     map[string]int
		updateKey string
		expectOK  bool
		expectVal int
	}{
		{name: "update existing key", setup: map[string]int{"a": 10}, updateKey: "a", expectOK: true, expectVal: 20},
		{name: "update missing key", setup: map[string]int{"a": 10}, updateKey: "z", expectOK: false, expectVal: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[int]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			ok := s.Update(tc.updateKey, func(v int) int { return v * 2 })
			assert.Equal(t, tc.expectOK, ok)

			val, found := s.Get(tc.updateKey)
			require.Equal(t, tc.expectOK, found)
			assert.Equal(t, tc.expectVal, val)
		})
	}
}

func TestStore_SetIfAbsent(t *testing.T) {
	tests := []struct {
		name      string
		setup     map[string]string
		key       string
		value     string
		expectSet bool
		expectVal string
	}{
		{name: "key absent", setup: map[string]string{}, key: "a", value: "new", expectSet: true, expectVal: "new"},
		{name: "key present", setup: map[string]string{"a": "old"}, key: "a", value: "new", expectSet: false, expectVal: "old"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[string]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			ok := s.SetIfAbsent(tc.key, tc.value)
			assert.Equal(t, tc.expectSet, ok)

			val, _ := s.Get(tc.key)
			assert.Equal(t, tc.expectVal, val)
		})
	}
}

func TestStore_Clear(t *testing.T) {
	s := New[int]()
	s.Set("a", 1)
	s.Set("b", 2)
	require.Equal(t, 2, s.Len())

	s.Clear()
	assert.Equal(t, 0, s.Len())
	assert.False(t, s.Has("a"))
}

func TestStore_Filter(t *testing.T) {
	tests := []struct {
		name   string
		setup  map[string]int
		pred   func(string, int) bool
		expect map[string]int
	}{
		{
			name:  "filter even values",
			setup: map[string]int{"a": 1, "b": 2, "c": 3, "d": 4},
			pred:  func(_ string, v int) bool { return v%2 == 0 },
			expect: map[string]int{"b": 2, "d": 4},
		},
		{
			name:   "no matches",
			setup:  map[string]int{"a": 1, "b": 3},
			pred:   func(_ string, v int) bool { return v > 10 },
			expect: map[string]int{},
		},
		{
			name:   "empty store",
			setup:  map[string]int{},
			pred:   func(_ string, _ int) bool { return true },
			expect: map[string]int{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New[int]()
			for k, v := range tc.setup {
				s.Set(k, v)
			}

			result := s.Filter(tc.pred)
			assert.Equal(t, tc.expect, result)
		})
	}
}

func TestStore_OverwriteValue(t *testing.T) {
	s := New[string]()
	s.Set("key", "first")
	s.Set("key", "second")

	val, ok := s.Get("key")
	assert.True(t, ok)
	assert.Equal(t, "second", val)
	assert.Equal(t, 1, s.Len())
}

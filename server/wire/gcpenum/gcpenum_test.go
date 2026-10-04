package gcpenum_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpenum"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

func testEnum(typ string, names ...string) gcpenum.Enum {
	m := make(map[int32]string, len(names))
	for i, n := range names {
		m[int32(i)] = n
	}

	return gcpenum.Enum{Type: typ, Names: m}
}

func testFields() gcpenum.Fields {
	mode := testEnum("test.Mode", "MODE_UNSPECIFIED", "STANDARD", "VIRTUAL")
	action := testEnum("test.Action", "ACTION_UNSPECIFIED", "DELETE", "KEEP")
	sev := testEnum("test.Severity", "SEVERITY_UNSPECIFIED", "ERROR", "WARNING")

	return gcpenum.Fields{
		"mode":                     mode,
		"stateMessages.severity":   sev,
		"cleanupPolicies.*.action": action,
		"days":                     sev,
		"labels.*":                 mode,
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "scalar", in: `{"mode":1}`, want: `{"mode":"STANDARD"}`},
		{name: "zero is unspecified", in: `{"mode":0}`, want: `{"mode":"MODE_UNSPECIFIED"}`},
		{name: "exponent integral", in: `{"mode":1e0}`, want: `{"mode":"STANDARD"}`},
		{name: "negative zero", in: `{"mode":-0}`, want: `{"mode":"MODE_UNSPECIFIED"}`},
		{
			name: "nested in array of objects",
			in:   `{"stateMessages":[{"severity":1},{"severity":2,"message":"x"}]}`,
			want: `{"stateMessages":[{"severity":"ERROR"},{"message":"x","severity":"WARNING"}]}`,
		},
		{
			name: "map key wildcard",
			in:   `{"cleanupPolicies":{"p1":{"action":1},"p2":{"action":2}}}`,
			want: `{"cleanupPolicies":{"p1":{"action":"DELETE"},"p2":{"action":"KEEP"}}}`,
		},
		{name: "map of enum", in: `{"labels":{"a":2}}`, want: `{"labels":{"a":"VIRTUAL"}}`},
		{name: "repeated enum", in: `{"days":[1,2]}`, want: `{"days":["ERROR","WARNING"]}`},
		{
			name: "non-enum number untouched",
			in:   `{"mode":1,"memorySizeGb":5}`,
			want: `{"memorySizeGb":5,"mode":"STANDARD"}`,
		},
		{
			name: "int64 precision kept",
			in:   `{"mode":1,"size":9007199254740993}`,
			want: `{"mode":"STANDARD","size":9007199254740993}`,
		},
		{name: "html not escaped", in: `{"mode":1,"d":"<a&b>"}`, want: `{"d":"<a&b>","mode":"STANDARD"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gcpenum.Normalize([]byte(tc.in), testFields())
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestNormalizeUnchangedReturnsSameSlice(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "strings only", in: `{"mode":"STANDARD","stateMessages":[{"severity":"ERROR"}]}`},
		{name: "no digits", in: `{"description":"x"}`},
		{name: "number at unknown path", in: `{"other":{"mode":3}}`},
		{name: "null enum", in: `{"mode":null}`},
		{name: "not an object", in: `[1,2]`},
		{name: "malformed", in: `{"mode":1`},
		{name: "empty", in: ``},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)

			got, err := gcpenum.Normalize(in, testFields())
			require.NoError(t, err)
			assert.Equal(t, tc.in, string(got))

			if len(in) > 0 {
				assert.Same(t, &in[0], &got[0], "unchanged body must be the input slice")
			}
		})
	}
}

func TestNormalizeUnknownValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		path string
	}{
		{name: "missing number", in: `{"mode":99}`, path: "mode"},
		{name: "fraction", in: `{"mode":1.5}`, path: "mode"},
		{name: "huge exponent", in: `{"mode":1e40}`, path: "mode"},
		{name: "negative with no entry", in: `{"mode":-1.0e0}`, path: "mode"},
		{name: "above int32", in: `{"mode":4294967297}`, path: "mode"},
		{name: "nested", in: `{"cleanupPolicies":{"p1":{"action":7}}}`, path: "cleanupPolicies.*.action"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gcpenum.Normalize([]byte(tc.in), testFields())

			var uv *gcpenum.UnknownValueError
			require.ErrorAs(t, err, &uv)
			assert.Equal(t, tc.path, uv.Path)
			assert.NotEmpty(t, uv.Value)
			assert.Contains(t, err.Error(), tc.path)
		})
	}
}

func TestNormalizeStored(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "known rewritten", in: `{"mode":2}`, want: `{"mode":"VIRTUAL"}`},
		{name: "unknown kept", in: `{"mode":99}`, want: `{"mode":99}`},
		{name: "fraction kept", in: `{"mode":1.5}`, want: `{"mode":1.5}`},
		{name: "known beside unknown", in: `{"mode":99,"days":[1]}`, want: `{"days":["ERROR"],"mode":99}`},
		{name: "malformed kept", in: `{"mode":`, want: `{"mode":`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, string(gcpenum.NormalizeStored([]byte(tc.in), testFields())))
		})
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := []byte(`{"mode":1,"cleanupPolicies":{"p1":{"action":2}},"days":[1,2]}`)
	orig := bytes.Clone(in)

	got, err := gcpenum.Normalize(in, testFields())
	require.NoError(t, err)
	assert.NotEqual(t, string(orig), string(got))
	assert.Equal(t, orig, in)

	gcpenum.NormalizeStored(in, testFields())
	assert.Equal(t, orig, in)
}

func TestSub(t *testing.T) {
	sub := gcpenum.Sub(testFields(), "cleanupPolicies")
	require.Len(t, sub, 1)
	assert.Equal(t, "test.Action", sub["*.action"].Type)

	got := gcpenum.NormalizeStored([]byte(`{"p1":{"action":1}}`), sub)
	assert.Equal(t, `{"p1":{"action":"DELETE"}}`, string(got))

	assert.Empty(t, gcpenum.Sub(testFields(), "description"))
	assert.Empty(t, gcpenum.Sub(testFields(), "clean"), "prefix must match a whole segment")
}

type decoded struct {
	Mode string `json:"mode"`
}

func decodeWith(body string, gcp bool) (int, decoded, string) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	var v decoded

	if gcp {
		gcprest.DecodeJSON(w, r, &v)
	} else {
		gcpenum.DecodeJSON(w, r, &v, testFields())
	}

	return w.Code, v, w.Body.String()
}

func TestDecodeJSONParity(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "trailing bytes", body: `{"mode":"STANDARD"} trailing`},
		{name: "empty body", body: ``},
		{name: "malformed", body: `{"mode":`},
		{name: "wrong type", body: `{"mode":true}`},
		{name: "string enum", body: `{"mode":"VIRTUAL"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantCode, wantV, wantBody := decodeWith(tc.body, true)
			gotCode, gotV, gotBody := decodeWith(tc.body, false)

			assert.Equal(t, wantCode, gotCode)
			assert.Equal(t, wantV, gotV)
			assert.Equal(t, wantBody, gotBody)
		})
	}
}

func TestDecodeJSONNumeric(t *testing.T) {
	code, v, _ := decodeWith(`{"mode":2}`, false)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "VIRTUAL", v.Mode)

	code, _, body := decodeWith(`{"mode":99}`, false)
	assert.Equal(t, http.StatusBadRequest, code)

	var env struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &env))
	assert.Equal(t, "INVALID_ARGUMENT", env.Error.Status)
	assert.Contains(t, env.Error.Message, "mode")
}

func TestReadBodyTooLarge(t *testing.T) {
	big := `{"description":"` + strings.Repeat("x", gcprest.MaxBodyBytes) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big))
	w := httptest.NewRecorder()

	_, ok := gcpenum.ReadBody(w, r, testFields())
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestName(t *testing.T) {
	mode := testEnum("test.Mode", "MODE_UNSPECIFIED", "STANDARD", "VIRTUAL")

	tests := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{name: "known string", raw: `"VIRTUAL"`, want: "VIRTUAL", wantOK: true},
		{name: "unknown string", raw: `"BOGUS"`},
		{name: "known number", raw: `1`, want: "STANDARD", wantOK: true},
		{name: "integral exponent", raw: `2e0`, want: "VIRTUAL", wantOK: true},
		{name: "unknown number", raw: `9`},
		{name: "fraction", raw: `1.5`},
		{name: "out of int32", raw: `1e40`},
		{name: "object", raw: `{"a":1}`},
		{name: "null", raw: `null`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := gcpenum.Name(json.RawMessage(tt.raw), mode)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

package gcprest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

func TestTypedAny(t *testing.T) {
	got, err := gcprest.TypedAny(struct {
		Name string `json:"name"`
	}{"n"}, "type.googleapis.com/x.Y")
	if err != nil || string(got) != `{"@type":"type.googleapis.com/x.Y","name":"n"}` {
		t.Fatalf("TypedAny = %s, %v", got, err)
	}

	if got, err = gcprest.TypedAny(struct{}{}, "type.googleapis.com/google.protobuf.Empty"); err != nil ||
		string(got) != `{"@type":"type.googleapis.com/google.protobuf.Empty"}` {
		t.Fatalf("gcprest.TypedAny(empty) = %s, %v", got, err)
	}

	if got, err = gcprest.TypedAny(nil, "t"); err != nil || string(got) != `{"@type":"t"}` {
		t.Fatalf("gcprest.TypedAny(nil) = %s, %v", got, err)
	}

	if _, err = gcprest.TypedAny([]int{1}, "t"); err == nil {
		t.Fatal("TypedAny of a non-object must fail")
	}

	if _, err = gcprest.TypedAny(make(chan int), "t"); err == nil {
		t.Fatal("TypedAny of an unmarshalable value must fail")
	}
}

func TestFormatTime(t *testing.T) {
	if gcprest.FormatTime(time.Time{}) != "" {
		t.Fatal("zero time must render empty")
	}

	ts := time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("x", 3600))
	if got := gcprest.FormatTime(ts); got != "2026-01-02T02:04:05.000000006Z" {
		t.Fatalf("FormatTime = %q", got)
	}
}

func TestDecodeOptionalJSON(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}

	for _, tc := range []struct {
		body string
		ok   bool
		code int
	}{
		{"", true, http.StatusOK},
		{`{"a":1}`, true, http.StatusOK},
		{`{`, false, http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))

		if ok := gcprest.DecodeOptionalJSON(w, r, &v); ok != tc.ok || w.Code != tc.code {
			t.Fatalf("body %q: ok=%v code=%d", tc.body, ok, w.Code)
		}
	}
}

package cloudwatch

import (
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

// TestCBORPayloadToJSON covers the awsJson1_0 value rules on a response:
// timestamps as epoch seconds, blobs as base64, doubles kept as doubles,
// empty lists and maps kept, and null or omitted members left out.
func TestCBORPayloadToJSON(t *testing.T) {
	type out struct {
		When    time.Time          `cbor:"When"`
		Image   []byte             `cbor:"MetricWidgetImage"`
		Empty   []string           `cbor:"Empty"`
		Stats   map[string]float64 `cbor:"Stats"`
		Skipped string             `cbor:"Skipped,omitempty"`
		Nil     *float64           `cbor:"Nil"`
		Sum     float64            `cbor:"Sum"`
		Period  int                `cbor:"Period"`
	}

	got, err := cborPayloadToJSON(out{
		When:   time.Unix(1700000000, 250*int64(time.Millisecond)),
		Image:  []byte("png-bytes"),
		Empty:  []string{},
		Stats:  map[string]float64{},
		Sum:    20,
		Period: 60,
	})
	if err != nil {
		t.Fatalf("cborPayloadToJSON: %v", err)
	}

	want := `{"Empty":[],"MetricWidgetImage":"cG5nLWJ5dGVz","Period":60,"Stats":{},"Sum":20.0,"When":1700000000.25}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// TestJSONToCBOR covers the request rules: integral numbers (60.0 too) decode
// into int fields, fractional numbers into floats, epoch seconds into
// time.Time, and trailing data is rejected.
func TestJSONToCBOR(t *testing.T) {
	type in struct {
		Period int        `cbor:"Period"`
		Value  float64    `cbor:"Value"`
		Whole  float64    `cbor:"Whole"`
		Start  *time.Time `cbor:"Start"`
		End    time.Time  `cbor:"End"`
	}

	body, err := jsonToCBOR([]byte(`{"Period":60.0,"Value":1.5,"Whole":3,"Start":1700000000,"End":1700000000.5}`))
	if err != nil {
		t.Fatalf("jsonToCBOR: %v", err)
	}

	var v in
	if err := cbor.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if v.Period != 60 || v.Value != 1.5 || v.Whole != 3 {
		t.Fatalf("numbers: %+v", v)
	}

	if v.Start == nil || v.Start.Unix() != 1700000000 || v.End.UnixMilli() != 1700000000500 {
		t.Fatalf("timestamps: %+v", v)
	}

	for _, bad := range []string{`[1]`, `{"a":`, `"x"`, `{"a":1} {"b":2}`, `{"a":1}x`, `{"a":1e999}`} {
		if _, err := jsonToCBOR([]byte(bad)); err == nil {
			t.Fatalf("jsonToCBOR(%s) must fail", bad)
		}
	}

	if _, err := jsonToCBOR(nil); err != nil {
		t.Fatalf("empty body: %v", err)
	}
}

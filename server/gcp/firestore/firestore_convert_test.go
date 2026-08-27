package firestore

import (
	"testing"
	"time"
)

// TestValueRoundTripScalars proves each scalar Firestore value type survives a
// Go<->wire round-trip with its type intact — the fidelity findings from the
// GCP audit (timestamp, bytes, reference, and integer-valued double).
func TestValueRoundTripScalars(t *testing.T) {
	// An integer-valued double must stay a doubleValue, not collapse to
	// integerValue (which would decode to int64 in the SDK).
	dv := goValueToFirestore(float64(30))
	if dv.DoubleValue == nil || dv.IntegerValue != nil {
		t.Errorf("float64(30) encoded as %+v, want doubleValue", dv)
	}

	if got, ok := firestoreValueToGo(dv).(float64); !ok || got != 30 {
		t.Errorf("double round-trip = %v (%T), want float64(30)", firestoreValueToGo(dv), firestoreValueToGo(dv))
	}

	// bytes -> bytesValue -> []byte.
	bv := goValueToFirestore([]byte("hello"))
	if bv.BytesValue == nil {
		t.Fatalf("[]byte encoded as %+v, want bytesValue", bv)
	}

	if got, ok := firestoreValueToGo(bv).([]byte); !ok || string(got) != "hello" {
		t.Errorf("bytes round-trip = %v (%T), want []byte(hello)", firestoreValueToGo(bv), firestoreValueToGo(bv))
	}

	// reference -> referenceValue -> reference (distinct from a plain string).
	const path = "projects/p/databases/(default)/documents/c/d"

	rv := goValueToFirestore(reference(path))
	if rv.ReferenceValue == nil {
		t.Fatalf("reference encoded as %+v, want referenceValue", rv)
	}

	if got, ok := firestoreValueToGo(rv).(reference); !ok || string(got) != path {
		t.Errorf("reference round-trip = %v (%T), want reference(%s)", firestoreValueToGo(rv), firestoreValueToGo(rv), path)
	}

	// time.Time -> timestampValue -> time.Time.
	ts := time.Date(2021, 5, 4, 3, 2, 1, 0, time.UTC)

	tv := goValueToFirestore(ts)
	if tv.TimestampValue == nil {
		t.Fatalf("time.Time encoded as %+v, want timestampValue", tv)
	}

	if got, ok := firestoreValueToGo(tv).(time.Time); !ok || !got.Equal(ts) {
		t.Errorf("timestamp round-trip = %v (%T), want %v", firestoreValueToGo(tv), firestoreValueToGo(tv), ts)
	}
}

// TestValueRoundTripGeoPoint proves geoPointValue survives the round-trip (the
// SDK decodes it to *latlng.LatLng, but that module is not a direct dependency,
// so the fidelity is asserted here at the conversion boundary instead).
func TestValueRoundTripGeoPoint(t *testing.T) {
	gp := &geoPoint{Latitude: 37.42, Longitude: -122.08}

	wire := goValueToFirestore(gp)
	if wire.GeoPointValue == nil {
		t.Fatalf("geoPoint encoded as %+v, want geoPointValue", wire)
	}

	got, ok := firestoreValueToGo(wire).(*geoPoint)
	if !ok {
		t.Fatalf("geoPoint round-trip type = %T, want *geoPoint", firestoreValueToGo(wire))
	}

	if got.Latitude != 37.42 || got.Longitude != -122.08 {
		t.Errorf("geoPoint round-trip = %+v, want {37.42,-122.08}", got)
	}
}

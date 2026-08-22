package cloudfunctions

import (
	"strconv"
	"testing"
)

func TestUploadStagingTakeConsumesOnce(t *testing.T) {
	s := newUploadStaging()
	s.stage("t1", []byte("code"))

	code, ok := s.take("t1")
	if !ok || string(code) != "code" {
		t.Fatalf("take = %q, %v", code, ok)
	}

	if _, ok := s.take("t1"); ok {
		t.Fatal("second take should miss")
	}
}

func TestUploadStagingEvictsOldest(t *testing.T) {
	s := newUploadStaging()

	// Stage the cap plus one; the very first token must have been evicted.
	s.stage("first", []byte("x"))
	for i := range maxStagedUploads {
		s.stage("t"+strconv.Itoa(i), []byte("x"))
	}

	if s.has("first") {
		t.Fatal("oldest entry should have been evicted")
	}

	// A recently staged token is still present.
	if !s.has("t" + strconv.Itoa(maxStagedUploads-1)) {
		t.Fatal("newest entry should still be present")
	}
}

func TestUploadStagingReplaceKeepsOneSlot(t *testing.T) {
	s := newUploadStaging()
	s.stage("t1", nil)           // generateUploadUrl stages an empty slot
	s.stage("t1", []byte("zip")) // uploadSource replaces it — not a new slot

	code, ok := s.take("t1")
	if !ok || string(code) != "zip" {
		t.Fatalf("take after replace = %q, %v", code, ok)
	}
}

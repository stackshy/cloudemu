package cloudfunctions

import (
	"errors"
	"sync"
)

// errUnresolvedUpload is returned when a create's sourceUploadUrl carries no
// token this server minted, or the token resolves to no staged bytes.
var errUnresolvedUpload = errors.New("unresolved source upload")

// maxStagedUploads bounds how many source-zip slots may be staged at once. A
// generateUploadUrl call that is never followed by create would otherwise leak a
// slot for the life of the (long-lived) daemon; past the cap the oldest slot is
// evicted so memory stays bounded.
const maxStagedUploads = 128

// uploadStaging holds source zips between generateUploadUrl (which stages an
// empty slot) and create (which takes the bytes). It is a bounded, FIFO-evicting
// store: self-contained here rather than reusing the GCS bucket mock, matching
// how real Cloud Functions uploads land in a private staging bucket.
type uploadStaging struct {
	mu   sync.Mutex
	seq  uint64
	byID map[string]stagedUpload
}

type stagedUpload struct {
	seq  uint64
	code []byte
}

func newUploadStaging() *uploadStaging {
	return &uploadStaging{byID: map[string]stagedUpload{}}
}

// stage records (or replaces) the bytes for token, evicting the oldest entry
// first when a new token would exceed the cap.
func (s *uploadStaging) stage(token string, code []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byID[token]; !exists && len(s.byID) >= maxStagedUploads {
		s.evictOldestLocked()
	}

	s.seq++
	s.byID[token] = stagedUpload{seq: s.seq, code: code}
}

// has reports whether token has a staged slot.
func (s *uploadStaging) has(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.byID[token]

	return ok
}

// take removes token's slot and returns its bytes.
func (s *uploadStaging) take(token string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.byID[token]
	delete(s.byID, token)

	return u.code, ok
}

func (s *uploadStaging) evictOldestLocked() {
	var (
		oldestTok string
		oldestSeq uint64
		found     bool
	)

	for tok, u := range s.byID {
		if !found || u.seq < oldestSeq {
			oldestTok, oldestSeq, found = tok, u.seq, true
		}
	}

	if found {
		delete(s.byID, oldestTok)
	}
}

package s3

import "sync"

// NameReservation is the process-wide S3 bucket-name namespace, shared across
// every regional S3 mock so bucket names are globally unique, as in
// real S3, where a bucket name taken in any region cannot be re-created in
// another. Each region owns its own bucket data plane (a separate *Mock), but
// they all consult one NameReservation, so CreateBucket rejects a name already
// held in ANY region with BucketAlreadyExists.
//
// A nil *NameReservation is inert: an S3 mock with no reservation wired enforces
// only its own per-mock uniqueness (the single-region library default), keeping
// the library path byte-for-byte identical to before multi-region isolation.
type NameReservation struct {
	mu    sync.Mutex
	names map[string]struct{}
}

// NewNameReservation returns an empty shared bucket-name namespace.
func NewNameReservation() *NameReservation {
	return &NameReservation{names: map[string]struct{}{}}
}

// reserve claims name for a bucket, returning false when it is already held (in
// this or any other region). A nil receiver always succeeds, so an unwired mock
// enforces only its own store's uniqueness.
func (r *NameReservation) reserve(name string) bool {
	if r == nil {
		return true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, taken := r.names[name]; taken {
		return false
	}

	r.names[name] = struct{}{}

	return true
}

// release frees name so it can be re-created (after DeleteBucket). A nil
// receiver and an unknown name are both no-ops.
func (r *NameReservation) release(name string) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.names, name)
}

package cosmosdb

import "sync"

// keyedMutex hands out one mutex per string key (a Cosmos container's qualified
// table name), created on first use. It serializes the compound check-then-write
// operations a single container needs — a document create's
// [documentExists + checkUniqueKeys + PutItem] must be one uninterruptible step,
// or two concurrent creates carrying the same (partition, unique-key value) can
// both pass the uniqueness check and both insert, violating Cosmos's 409
// guarantee. The same per-container lock also serializes replaces, deletes and
// TTL reaping so a lazy expiry sweep can never delete a document a concurrent
// write just resurrected.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*sync.Mutex)}
}

// lock acquires the mutex for key and returns its unlock func. The returned
// func must be called (typically via defer) to release the lock.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()

	m, ok := k.locks[key]
	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}

	k.mu.Unlock()

	m.Lock()

	return m.Unlock
}

package idgen

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdvanceToMovesCounterForward(t *testing.T) {
	Reset()

	AdvanceTo(0x2a)
	assert.Equal(t, uint64(0x2a), Counter())
	assert.Equal(t, "i-0000002b", GenerateID("i-"))
	assert.True(t, strings.HasSuffix(OCID("vcn", "", "us-ashburn-1"), "aaaaaaaa000000000000002c"))
}

func TestAdvanceToNeverRewinds(t *testing.T) {
	Reset()
	AdvanceTo(100)

	AdvanceTo(5)
	assert.Equal(t, uint64(100), Counter())

	AdvanceTo(100)
	assert.Equal(t, uint64(100), Counter())
}

// TestAdvanceToConcurrentWithGenerateID runs AdvanceTo against GenerateID from
// many goroutines (run with -race): ids stay unique and the counter ends at
// least at the highest target.
func TestAdvanceToConcurrentWithGenerateID(t *testing.T) {
	Reset()

	const (
		workers = 8
		perW    = 500
	)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = make(map[string]bool, workers*perW)
	)

	for w := range workers {
		wg.Add(2)

		go func() {
			defer wg.Done()

			local := make([]string, 0, perW)
			for range perW {
				local = append(local, GenerateID("x-"))
			}

			mu.Lock()
			defer mu.Unlock()

			for _, id := range local {
				assert.False(t, seen[id], "duplicate id %s", id)
				seen[id] = true
			}
		}()

		go func() {
			defer wg.Done()

			for i := range perW {
				AdvanceTo(uint64(w*perW + i))
			}
		}()
	}

	wg.Wait()

	assert.Len(t, seen, workers*perW)
	assert.GreaterOrEqual(t, Counter(), uint64(workers*perW-1))
}

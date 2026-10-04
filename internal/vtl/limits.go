package vtl

import (
	"errors"
	"strings"
)

// Size and depth limits. A template that hits one fails with an error instead
// of growing without bound.
const (
	// MaxTemplateBytes is the largest template Parse accepts: API Gateway's
	// 300 KB mapping-template quota.
	MaxTemplateBytes = 300 << 10
	// MaxOutputBytes caps a render's output and any single string value: API
	// Gateway's 10 MB payload quota.
	MaxOutputBytes = 10 << 20
	// MaxAllocBytes caps the strings and collection entries one render may
	// create, so a loop that keeps doubling a value fails fast.
	MaxAllocBytes = 64 << 20
	// MaxTemplateDepth caps how deeply directives, expressions and string
	// interpolations may nest.
	MaxTemplateDepth = 100
	// MaxValueDepth caps how deeply printing, encoding, comparing and JSON
	// parsing descend into nested lists and maps.
	MaxValueDepth = 1000
	// maxOperatorChain caps a run of binary operators, which the evaluator
	// walks recursively.
	maxOperatorChain = 1000
	// slotBytes is what one list or map entry is charged against
	// MaxAllocBytes: the 16-byte interface plus room for slice growth.
	slotBytes = 32
)

// Limit errors.
var (
	ErrOutputLimit = errors.New("vtl: output exceeds the maximum size")
	ErrMemoryLimit = errors.New("vtl: template exceeded its memory budget")
	ErrDepthLimit  = errors.New("vtl: value nesting exceeds the maximum depth")
	ErrCyclicValue = errors.New("vtl: cannot encode a value that contains itself")
)

// budget tracks the bytes a render has created.
type budget struct {
	used int
}

func (b *budget) charge(n int) error {
	b.used += n
	if b.used > MaxAllocBytes {
		return ErrMemoryLimit
	}

	return nil
}

// sizeOf estimates the bytes a value holds, stopping once it passes limit.
// Shared and cyclic parts may be counted more than once, which only makes the
// estimate conservative.
func sizeOf(v any, limit int) int {
	total := 0
	stack := []any{v}
	visits := 0

	for len(stack) > 0 && total <= limit && visits <= MaxAllocBytes/slotBytes {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visits++

		switch t := cur.(type) {
		case string:
			total += len(t)
		case *List:
			total += len(t.Items) * slotBytes
			stack = append(stack, t.Items...)
		case *Map:
			total += t.Len() * slotBytes

			for _, k := range t.keys {
				total += len(k)
				stack = append(stack, t.vals[k])
			}
		default:
			total += slotBytes
		}
	}

	return total
}

// boundedWriter is a strings.Builder that refuses to grow past limit.
type boundedWriter struct {
	b     strings.Builder
	limit int
}

func (w *boundedWriter) WriteString(s string) error {
	if w.b.Len()+len(s) > w.limit {
		return ErrOutputLimit
	}

	w.b.WriteString(s)

	return nil
}

func (w *boundedWriter) WriteByte(c byte) error {
	return w.WriteString(string(c))
}

func (w *boundedWriter) String() string { return w.b.String() }

func (w *boundedWriter) Len() int { return w.b.Len() }

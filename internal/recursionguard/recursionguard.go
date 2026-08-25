// Package recursionguard bounds cross-service re-entrant call chains — e.g. a
// DynamoDB Streams -> Lambda event source mapping whose handler writes back
// into its own source table (mark-processed, audit-append, status-bump). Left
// unbounded, that pattern recurses synchronously on the same goroutine
// (PutItem -> deliver -> Invoke -> handler -> PutItem -> ...) until it blows
// the goroutine stack, which crashes the whole process with an unrecoverable
// "fatal error: stack overflow" that recover() cannot catch.
//
// The cap mirrors AWS Lambda's own recursive-loop detection: Lambda stops
// invoking a function once it has been invoked approximately 16 times within
// the same chain of requests, and reports the drop via the
// RecursiveInvocationsDropped metric. See "Use Lambda recursive loop
// detection to prevent infinite loops":
// https://docs.aws.amazon.com/lambda/latest/dg/invocation-recursion.html
package recursionguard

import "context"

// MaxDepth is the maximum number of nested deliveries permitted within one
// chain of requests before further delivery is skipped, matching AWS
// Lambda's documented recursive-loop-detection threshold of ~16 invocations.
const MaxDepth = 16

// DepthHeader carries the re-entrant delivery depth across a delivery hop that
// crosses an HTTP boundary. In-process chains ride the goroutine's ctx, but a
// hop like Event Grid WebHook delivery is a fresh outbound HTTP request whose
// response re-enters the emulator through a separate handler goroutine, so the
// ctx depth can't ride along. The sender stamps the incremented depth on this
// header; the re-entered publish handler seeds ctx from it via WithDepth so the
// chain keeps counting toward MaxDepth instead of resetting to zero each hop.
const DepthHeader = "X-Cloudemu-Delivery-Depth"

type depthKey struct{}

// WithDepth returns a copy of ctx carrying the given re-entrant delivery
// depth for the current chain of requests.
func WithDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, depthKey{}, depth)
}

// Depth returns the re-entrant delivery depth carried on ctx, or 0 if ctx
// carries none (i.e. this is the start of a new chain of requests).
func Depth(ctx context.Context) int {
	depth, _ := ctx.Value(depthKey{}).(int)

	return depth
}

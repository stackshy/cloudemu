// Package aws holds CloudEmu's AWS SDK-compatibility suite.
//
// The tests live in *_test.go files and run real aws-sdk-go-v2 clients against
// the in-process wire server via the shared harness in internal/compat. The
// matrix generator (internal/compatgen) turns their recorded results into
// docs/compat/.
package aws

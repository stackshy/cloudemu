package asl

import (
	"math"
	"time"
)

// ASL Retrier defaults applied when a field is omitted.
const (
	defaultRetryInterval = 1.0
	defaultRetryMax      = 3
	defaultBackoffRate   = 2.0
	minBackoffRate       = 1.0
	errAll               = "States.ALL"
)

// Retrier is one entry in a state's Retry array. MaxDelaySeconds and
// JitterStrategy are parsed but deliberately NOT honored yet (a documented
// deferred no-op) so a definition using them is accepted without silently
// changing backoff timing.
type Retrier struct {
	ErrorEquals     []string `json:"ErrorEquals"`
	IntervalSeconds *float64 `json:"IntervalSeconds"`
	MaxAttempts     *int     `json:"MaxAttempts"`
	BackoffRate     *float64 `json:"BackoffRate"`
	MaxDelaySeconds *int     `json:"MaxDelaySeconds"` // deferred no-op
	JitterStrategy  string   `json:"JitterStrategy"`  // deferred no-op
}

// Catcher is one entry in a state's Catch array: a matching error transitions to
// Next with the {Error,Cause} error output merged at ResultPath.
type Catcher struct {
	ErrorEquals []string  `json:"ErrorEquals"`
	Next        string    `json:"Next"`
	ResultPath  pathField `json:"ResultPath"`
}

// runWithRetry runs attempt, retrying on a matching Retrier until it succeeds or
// the matched Retrier's MaxAttempts is exhausted. Backoff advances the virtual
// clock (config.Clock), so timing is FakeClock-deterministic and folds into the
// settle window under AsyncSettle. It is shared by Task (re-invoking the Lambda),
// Parallel (re-running all branches), and Map (re-running all iterations).
func (it *interp) runWithRetry(st *State, attempt func() (any, *stateError)) (any, *stateError) {
	attempts := make([]int, len(st.Retry))

	for {
		result, se := attempt()
		if se == nil {
			return result, nil
		}

		idx := matchRetrier(st.Retry, se.Code)
		if idx < 0 || attempts[idx] >= retrierMaxAttempts(st.Retry[idx]) {
			return nil, se
		}

		it.backoff(retrierDelay(st.Retry[idx], attempts[idx]))

		attempts[idx]++
	}
}

// tryCatch finds the first Catcher matching se and, on a match, returns the
// error output merged at the Catcher's ResultPath and the Catcher's Next.
func tryCatch(st *State, raw any, se *stateError) (out any, next string, ok bool) {
	for _, c := range st.Catch {
		if !errorMatches(c.ErrorEquals, se.Code) {
			continue
		}

		errOut := map[string]any{"Error": se.Code, "Cause": se.Cause}

		merged, err := mergeResultPath(c.ResultPath, raw, errOut)
		if err != nil {
			merged = errOut
		}

		return merged, c.Next, true
	}

	return nil, "", false
}

// matchRetrier returns the index of the first Retrier whose ErrorEquals matches
// code, or -1. Per the ASL spec the first match wins; a later Retrier is not
// consulted even if the matched one's attempts are exhausted.
func matchRetrier(retriers []*Retrier, code string) int {
	for i, r := range retriers {
		if errorMatches(r.ErrorEquals, code) {
			return i
		}
	}

	return -1
}

// errorMatches reports whether an error code satisfies an ErrorEquals set;
// States.ALL matches any error.
func errorMatches(errorEquals []string, code string) bool {
	for _, e := range errorEquals {
		if e == errAll || e == code {
			return true
		}
	}

	return false
}

// retrierMaxAttempts is the Retrier's MaxAttempts, defaulting to 3. A value of 0
// means the error is never retried.
func retrierMaxAttempts(r *Retrier) int {
	if r.MaxAttempts == nil {
		return defaultRetryMax
	}

	return *r.MaxAttempts
}

// retrierDelay is IntervalSeconds * BackoffRate^attempt (attempt counts prior
// retries of this Retrier), with the ASL defaults applied when fields are unset.
func retrierDelay(r *Retrier, attempt int) time.Duration {
	interval := defaultRetryInterval
	if r.IntervalSeconds != nil {
		interval = *r.IntervalSeconds
	}

	rate := defaultBackoffRate
	if r.BackoffRate != nil {
		rate = *r.BackoffRate
	}

	secs := interval * math.Pow(rate, float64(attempt))

	return time.Duration(secs * float64(time.Second))
}

// backoff advances the interpreter's virtual clock offset and Wait/Retry total.
func (it *interp) backoff(d time.Duration) {
	it.offset += d
	it.waitTotal += d
}

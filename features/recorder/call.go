// Package recorder provides call recording (VCR pattern) for cloudemu services.
package recorder

import "time"

// Call represents a recorded API call.
type Call struct {
	Service   string
	Operation string
	Input     any
	Output    any
	Error     error
	Timestamp time.Time
	Duration  time.Duration
}

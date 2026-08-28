package asl

import (
	"context"
	"fmt"
	"time"
)

// waitHandler runs a Wait state: it advances the virtual clock offset by the
// wait duration (so later history events sit past it, and the settle window
// extends under AsyncSettle), then passes the input through.
func waitHandler(_ context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	dur, err := it.waitDuration(st, raw)
	if err != nil {
		return nil, "", false, err
	}

	it.offset += dur
	it.waitTotal += dur

	out, err = it.passThroughOutput(st, raw)
	if err != nil {
		return nil, "", false, err
	}

	it.exitState(st, out)

	return out, st.Next, st.End, nil
}

// waitDuration resolves a Wait state's delay from Seconds/SecondsPath (relative)
// or Timestamp/TimestampPath (absolute, clamped to the virtual now).
func (it *interp) waitDuration(st *State, raw any) (time.Duration, error) {
	switch {
	case st.Seconds != nil:
		return time.Duration(*st.Seconds) * time.Second, nil
	case st.SecondsPath != "":
		return it.secondsFromPath(st.SecondsPath, raw)
	case st.Timestamp != "":
		return it.untilTimestamp(st.Timestamp)
	case st.TimestampPath != "":
		return it.timestampFromPath(st.TimestampPath, raw)
	default:
		return 0, &stateError{Code: "States.Runtime",
			Cause: fmt.Sprintf("Wait state %q has no Seconds/SecondsPath/Timestamp/TimestampPath", st.name)}
	}
}

func (it *interp) secondsFromPath(path string, raw any) (time.Duration, error) {
	v, present, err := it.resolvePath(path, raw)
	if err != nil {
		return 0, err
	}

	secs, ok := toFloat(v)
	if !present || !ok {
		return 0, &stateError{Code: "States.Runtime",
			Cause: fmt.Sprintf("SecondsPath %q did not resolve to a number", path)}
	}

	return time.Duration(secs) * time.Second, nil
}

func (it *interp) timestampFromPath(path string, raw any) (time.Duration, error) {
	v, present, err := it.resolvePath(path, raw)
	if err != nil {
		return 0, err
	}

	ts, ok := v.(string)
	if !present || !ok {
		return 0, &stateError{Code: "States.Runtime",
			Cause: fmt.Sprintf("TimestampPath %q did not resolve to a timestamp", path)}
	}

	return it.untilTimestamp(ts)
}

func (it *interp) untilTimestamp(ts string) (time.Duration, error) {
	target, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0, &stateError{Code: "States.Runtime", Cause: fmt.Sprintf("invalid Wait timestamp %q", ts)}
	}

	d := target.Sub(it.baseTime.Add(it.offset))
	if d < 0 {
		return 0, nil
	}

	return d, nil
}

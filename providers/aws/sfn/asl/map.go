package asl

import (
	"context"
	"fmt"
)

// mapHandler runs a Map state: it resolves the items array (ItemsPath, or the
// whole effective input when absent) and runs the ItemProcessor/Iterator
// sub-state-machine once per item, sequentially, applying ItemSelector to shape
// each iteration's input. The state's result is the array of per-iteration
// outputs in order. Any iteration failure fails the Map, which — with the state's
// own I/O-pipeline errors — flows through the state's Retry (re-running all
// iterations) and Catch. MaxConcurrency is parsed but not honored (execution is
// sequential). ctx threads into every iteration so a Map -> Task -> Lambda cycle
// stays bounded by the recursion guard.
func mapHandler(ctx context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	out, se := it.runWithRetry(st, func() (any, *stateError) {
		return it.runMap(ctx, st, raw)
	})
	if se != nil {
		return catchOrFail(st, raw, se)
	}

	it.exitState(st, out)

	return out, st.Next, st.End, nil
}

// runMap is one Map attempt: it resolves the items, runs the processor per item,
// and threads the ordered iteration outputs through the result pipeline
// (ResultSelector -> ResultPath onto the RAW input -> OutputPath).
func (it *interp) runMap(ctx context.Context, st *State, raw any) (any, *stateError) {
	if se := it.enterNesting(); se != nil {
		return nil, se
	}
	defer it.leaveNesting()

	input, se := it.stateInput(st, raw)
	if se != nil {
		return nil, se
	}

	items, se := it.resolveItems(st, input)
	if se != nil {
		return nil, se
	}

	proc := st.processor()

	results := make([]any, 0, len(items))

	for idx, item := range items {
		iterIn, ise := it.applyItemSelector(st, input, item, idx)
		if ise != nil {
			return nil, ise
		}

		iterOut, rse := it.runGraph(ctx, proc.StartAt, proc.States, iterIn)
		if rse != nil {
			return nil, rse
		}

		results = append(results, iterOut)
	}

	return it.resultPipeline(st, raw, results)
}

// resolveItems yields the array a Map iterates over: ItemsPath resolved against
// the effective input, else a literal Items array, else the effective input
// itself. A non-array or a missing ItemsPath fails loudly (feeding Retry/Catch).
func (it *interp) resolveItems(st *State, input any) ([]any, *stateError) {
	src, se := it.mapItemsSource(st, input)
	if se != nil {
		return nil, se
	}

	arr, ok := src.([]any)
	if !ok {
		return nil, &stateError{Code: "States.Runtime",
			Cause: fmt.Sprintf("Map state %q items did not resolve to an array", st.name)}
	}

	return arr, nil
}

func (it *interp) mapItemsSource(st *State, input any) (any, *stateError) {
	switch {
	case st.ItemsPath != "":
		v, present, err := it.resolvePath(st.ItemsPath, input)
		if err != nil {
			return nil, asStateError(err)
		}

		if !present {
			return nil, &stateError{Code: "States.Runtime",
				Cause: fmt.Sprintf("ItemsPath %q could not be found in the input", st.ItemsPath)}
		}

		return v, nil
	case st.Items != nil:
		v, err := rawToValue(st.Items)
		if err != nil {
			return nil, asStateError(err)
		}

		return v, nil
	default:
		return input, nil
	}
}

// applyItemSelector shapes one iteration's input. Absent ItemSelector passes the
// item through unchanged; otherwise the ItemSelector payload template resolves
// against the Map's effective input with $$.Map.Item.{Index,Value} bound to the
// current iteration.
func (it *interp) applyItemSelector(st *State, input, item any, idx int) (any, *stateError) {
	if st.ItemSelector == nil {
		return item, nil
	}

	it.setMapContext(idx, item)
	defer it.clearMapContext()

	v, err := it.applyPayloadTemplate(st.ItemSelector, input)
	if err != nil {
		return nil, asStateError(err)
	}

	return v, nil
}

// setMapContext binds $$.Map.Item.{Index,Value} for an ItemSelector evaluation.
func (it *interp) setMapContext(idx int, item any) {
	it.ctxObj["Map"] = map[string]any{
		"Item": map[string]any{"Index": float64(idx), "Value": item},
	}
}

// clearMapContext removes the $$.Map binding once the iteration input is built.
func (it *interp) clearMapContext() {
	delete(it.ctxObj, "Map")
}

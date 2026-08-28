package asl

import "context"

// parallelHandler runs a Parallel state: each branch is its own sub-state-machine
// run, sequentially, on the SAME effective input (InputPath -> Parameters). The
// state's result is the JSON array of branch outputs in branch order. Any branch
// failure fails the Parallel state with that branch's error, which — together
// with the state's own I/O-pipeline errors — flows through the state's Retry
// (re-running all branches) and Catch. ctx threads into every branch run, so a
// Parallel -> Task -> Lambda cycle stays bounded by the recursion guard.
func parallelHandler(ctx context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	out, se := it.runWithRetry(st, func() (any, *stateError) {
		return it.runParallel(ctx, st, raw)
	})
	if se != nil {
		return catchOrFail(st, raw, se)
	}

	it.exitState(st, out)

	return out, st.Next, st.End, nil
}

// runParallel is one Parallel attempt: it runs every branch to completion on the
// shared effective input and threads the ordered branch outputs through the
// result pipeline (ResultSelector -> ResultPath onto the RAW input -> OutputPath).
func (it *interp) runParallel(ctx context.Context, st *State, raw any) (any, *stateError) {
	if se := it.enterNesting(); se != nil {
		return nil, se
	}
	defer it.leaveNesting()

	input, se := it.stateInput(st, raw)
	if se != nil {
		return nil, se
	}

	results := make([]any, 0, len(st.Branches))

	for _, br := range st.Branches {
		branchOut, bse := it.runGraph(ctx, br.StartAt, br.States, input)
		if bse != nil {
			return nil, bse
		}

		results = append(results, branchOut)
	}

	return it.resultPipeline(st, raw, results)
}

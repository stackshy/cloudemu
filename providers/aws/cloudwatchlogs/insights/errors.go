package insights

// CompileError is a query that does not parse, with the character offsets of
// the offending text, the shape of the API's QueryCompileError.
type CompileError struct {
	Start, End int
	Message    string
}

func (e *CompileError) Error() string { return e.Message }

func compileErr(start, end int, msg string) *CompileError {
	return &CompileError{Start: start, End: end, Message: msg}
}

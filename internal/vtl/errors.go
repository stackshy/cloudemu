package vtl

import "fmt"

// Error is a template evaluation error, such as an invalid regular expression
// passed to a string method.
type Error struct {
	Msg string
}

func (e *Error) Error() string { return "vtl: " + e.Msg }

func errorf(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

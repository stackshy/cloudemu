package cloudwatch

import "errors"

// Request error codes that CloudWatch returns with a 400.
const (
	errInvalidNextToken      = "InvalidNextToken"
	errInvalidParameterValue = "InvalidParameterValue"
	errMissingParameter      = "MissingParameter"
	errValidation            = "ValidationError"
)

// wireError is a request error that carries a real CloudWatch error code.
// Cores return it and both codecs write it as a 400 with that code.
type wireError struct {
	code string
	msg  string
}

func (e *wireError) Error() string { return e.msg }

func newWireError(code, msg string) error {
	return &wireError{code: code, msg: msg}
}

// asWireError reports whether err is a wireError.
func asWireError(err error) (*wireError, bool) {
	var we *wireError
	if errors.As(err, &we) {
		return we, true
	}

	return nil, false
}

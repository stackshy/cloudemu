package cloudwatch

import (
	"errors"
	"net/http"
)

// Request error codes that CloudWatch returns with a 400.
const (
	errInvalidNextToken      = "InvalidNextToken"
	errInvalidParameterValue = "InvalidParameterValue"
	errInvalidParameterCombo = "InvalidParameterCombination"
	errLimitExceeded         = "LimitExceededException"
	errMissingParameter      = "MissingParameter"
	errValidation            = "ValidationError"
)

// errResourceNotFoundException is the 404 code of the anomaly detector ops.
const errResourceNotFoundException = "ResourceNotFoundException"

// wireError is a request error that carries a real CloudWatch error code and
// HTTP status. Cores return it and both codecs write it as is.
type wireError struct {
	code   string
	msg    string
	status int
}

func (e *wireError) Error() string { return e.msg }

// newWireError is a 400 with the given code.
func newWireError(code, msg string) error {
	return &wireError{code: code, msg: msg, status: http.StatusBadRequest}
}

// newNotFoundError is a 404 with the given code.
func newNotFoundError(code, msg string) error {
	return &wireError{code: code, msg: msg, status: http.StatusNotFound}
}

// asWireError reports whether err is a wireError.
func asWireError(err error) (*wireError, bool) {
	var we *wireError
	if errors.As(err, &we) {
		return we, true
	}

	return nil, false
}

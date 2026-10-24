package cloudwatch

import (
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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
// HTTP status. Cores return it and both codecs write it as is. shape is the
// model error shape name when it differs from the query code: the JSON and
// rpc-v2-cbor codecs put it in __type (and the code in X-Amzn-Query-Error),
// the query codec writes the code.
type wireError struct {
	code   string
	shape  string
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

// Dashboard not-found: the CloudWatch model's DashboardNotFoundError shape,
// whose awsQuery code is ResourceNotFound with HTTP 404.
const (
	errResourceNotFound       = "ResourceNotFound"
	shapeDashboardNotFoundErr = "DashboardNotFoundError"
)

// dashboardErr turns a NotFound from the dashboard store into the
// DashboardNotFoundError shape. GetDashboard and DeleteDashboards list it
// instead of the generic ResourceNotFound shape, so an SDK only maps the
// error to types.DashboardNotFoundError when __type names this shape.
func dashboardErr(err error) error {
	if err == nil || !cerrors.IsNotFound(err) {
		return err
	}

	return &wireError{
		code: errResourceNotFound, shape: shapeDashboardNotFoundErr,
		msg: cerrors.Message(err), status: http.StatusNotFound,
	}
}

// asWireError reports whether err is a wireError.
func asWireError(err error) (*wireError, bool) {
	var we *wireError
	if errors.As(err, &we) {
		return we, true
	}

	return nil, false
}

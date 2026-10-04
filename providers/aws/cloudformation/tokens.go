package cloudformation

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Actions a ClientRequestToken is recorded under.
const (
	actionCreateStack            = "CreateStack"
	actionUpdateStack            = "UpdateStack"
	actionDeleteStack            = "DeleteStack"
	actionCancelUpdateStack      = "CancelUpdateStack"
	actionContinueUpdateRollback = "ContinueUpdateRollback"
	actionRollbackStack          = "RollbackStack"
	actionExecuteChangeSet       = "ExecuteChangeSet"
)

const msgTokenExists = "A client request token [%s] already exists for a different request on stack [%s]."

// checkToken looks a request's ClientRequestToken up on the stack. retry is
// true when the same action already ran with it, so the request is a retry
// that must not run again. A token another action used is a
// TokenAlreadyExistsException.
func (sd *stackData) checkToken(token, action string) (retry bool, err error) {
	if token == "" {
		return false, nil
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	used, ok := sd.tokens[token]
	if !ok {
		return false, nil
	}

	if used != action {
		return false, cfn.NewException(cfn.ExceptionTokenAlreadyExists,
			cerrors.Newf(cerrors.FailedPrecondition, msgTokenExists, token, sd.stack.Name))
	}

	return true, nil
}

// recordToken records the token of an operation that starts. The events it
// records carry the token. The caller holds sd.mu.
func (sd *stackData) recordToken(token, action string) {
	sd.opToken = token

	if token == "" {
		return
	}

	if sd.tokens == nil {
		sd.tokens = map[string]string{}
	}

	sd.tokens[token] = action
}

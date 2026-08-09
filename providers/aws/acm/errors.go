package acm

import "github.com/stackshy/cloudemu/v2/errors"

func errNotFound(arn string) error {
	return errors.Newf(errors.NotFound, "certificate %q not found", arn)
}

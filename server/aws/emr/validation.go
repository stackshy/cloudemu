package emr

import (
	"fmt"
	"regexp"
)

// releaseLabelPattern is the shape of an EMR release label, e.g. emr-6.15.0.
var releaseLabelPattern = regexp.MustCompile(`^emr-\d+\.\d+\.\d+$`)

// validationError is an input check that failed. It maps to the EMR
// ValidationException wire code.
type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }

func validationErrorf(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

// validateRunJobFlow checks the fields RunJobFlow needs before a cluster is
// created. Name and Instances are required. Name may be empty.
func validateRunJobFlow(in *runJobFlowInput) error {
	if in.Name == nil {
		return validationErrorf("1 validation error detected: Value null at 'name' failed to satisfy constraint: " +
			"Member must not be null")
	}

	if in.Instances == nil {
		return validationErrorf("1 validation error detected: Value null at 'instances' failed to satisfy constraint: " +
			"Member must not be null")
	}

	if in.ReleaseLabel != nil && !releaseLabelPattern.MatchString(*in.ReleaseLabel) {
		return validationErrorf("The supplied release label is invalid: %s.", *in.ReleaseLabel)
	}

	if n := in.Instances.InstanceCount; n != nil && *n < 1 {
		return validationErrorf("InstanceCount must be at least 1, got %d.", *n)
	}

	return validateGroupCounts(in.Instances.InstanceGroups, 1)
}

// validateGroupCounts rejects an instance group whose InstanceCount is below
// minCount.
func validateGroupCounts(groups []instanceGroupConfigInput, minCount int32) error {
	for _, g := range groups {
		if g.InstanceCount != nil && *g.InstanceCount < minCount {
			return validationErrorf("InstanceCount for instance group %q must be at least %d, got %d.",
				deref(g.Name), minCount, *g.InstanceCount)
		}
	}

	return nil
}

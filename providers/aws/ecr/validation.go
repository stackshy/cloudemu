package ecr

import (
	"regexp"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Repository name limits from the CreateRepository API reference.
const (
	minRepoNameLen = 2
	maxRepoNameLen = 256
)

// repoNamePattern is the repository name pattern from the ECR API reference.
var repoNamePattern = regexp.MustCompile(
	`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*$`)

// validateRepositoryName checks a new repository name's length and pattern.
func validateRepositoryName(name string) error {
	if name == "" {
		return errors.New(errors.InvalidArgument, "repository name is required")
	}

	if len(name) < minRepoNameLen || len(name) > maxRepoNameLen || !repoNamePattern.MatchString(name) {
		return errors.Newf(errors.InvalidArgument,
			"Invalid parameter at 'repositoryName' failed to satisfy constraint: "+
				"'must satisfy regular expression '%s' and be %d to %d characters long'",
			repoNamePattern.String(), minRepoNameLen, maxRepoNameLen)
	}

	return nil
}

package apprunner

import (
	"strconv"
	"strings"
)

// arnResource returns the resource portion of an AWS ARN (everything after the
// sixth colon-separated field), or "" when the ARN is malformed.
func arnResource(arn string) string {
	const arnFields = 6

	parts := strings.SplitN(arn, ":", arnFields)
	if len(parts) < arnFields {
		return ""
	}

	return parts[arnFields-1]
}

// autoScalingRefFromARN extracts the (name, revision) pair from an auto scaling
// configuration ARN whose resource is autoscalingconfiguration/<name>/<rev>/<id>.
// A name-only ARN (no revision) yields the latest-revision marker 0.
func autoScalingRefFromARN(arn string) (name string, revision int32) {
	const (
		nameSegments     = 2
		revisionSegments = 4
		revisionBits     = 32
	)

	segs := strings.Split(arnResource(arn), "/")
	if len(segs) < nameSegments {
		return "", 0
	}

	name = segs[1]

	if len(segs) >= revisionSegments {
		if r, err := strconv.ParseInt(segs[2], 10, revisionBits); err == nil {
			revision = int32(r)
		}
	}

	return name, revision
}

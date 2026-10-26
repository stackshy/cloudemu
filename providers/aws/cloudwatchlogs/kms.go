package cloudwatchlogs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// KeyResolver looks up a KMS key by ID, ARN, or alias. The KMS mock satisfies
// it, so a key named on CreateLogGroup or AssociateKmsKey must really exist.
type KeyResolver interface {
	DescribeKey(ctx context.Context, keyID string) (*kmsdriver.KeyMetadata, error)
}

// SetKeyResolver wires KMS so log group keys are checked against real keys.
// Without it any well-formed key ARN is accepted.
func (m *Mock) SetKeyResolver(r KeyResolver) {
	m.keys = r
}

const (
	logGroupARNMarker   = ":log-group:"
	queryResultARNPart  = ":query-result:"
	queryResultWildcard = "*"
)

// validateKmsKey applies the CloudWatch Logs key rules: the key must be a KMS
// key ARN and, when KMS is wired, an existing enabled key. Any failure is an
// InvalidParameterException, which is what AWS returns for a missing or
// disabled key.
func (m *Mock) validateKmsKey(ctx context.Context, keyID, targetARN string) error {
	bad := errors.Newf(errors.InvalidArgument,
		"Specified KMS key %s does not exist or is not allowed to be used with Arn '%s'", keyID, targetARN)

	if !strings.HasPrefix(keyID, "arn:") || !strings.Contains(keyID, ":kms:") {
		return bad
	}

	if m.keys == nil {
		return nil
	}

	meta, err := m.keys.DescribeKey(ctx, keyID)
	if err != nil || meta == nil || !meta.Enabled || meta.KeyState != kmsdriver.StateEnabled {
		return bad
	}

	return nil
}

// AssociateKmsKey points a log group, or the account's Logs Insights query
// results, at a KMS key.
func (m *Mock) AssociateKmsKey(ctx context.Context, logGroup, resourceIdentifier, kmsKeyID string) error {
	if kmsKeyID == "" {
		return errors.New(errors.InvalidArgument, "kmsKeyId is required")
	}

	target, err := m.resolveKmsTarget(logGroup, resourceIdentifier)
	if err != nil {
		return err
	}

	if err := m.validateKmsKey(ctx, kmsKeyID, target.arn); err != nil {
		return err
	}

	return m.setKmsKey(target, kmsKeyID)
}

// DisassociateKmsKey removes the key from a log group or from query results.
// Disassociating a target that has no key is not an error.
func (m *Mock) DisassociateKmsKey(_ context.Context, logGroup, resourceIdentifier string) error {
	target, err := m.resolveKmsTarget(logGroup, resourceIdentifier)
	if err != nil {
		return err
	}

	return m.setKmsKey(target, "")
}

// kmsTarget is either a log group (group set) or the query-result resource.
type kmsTarget struct {
	group string
	arn   string
}

func (m *Mock) resolveKmsTarget(logGroup, resourceIdentifier string) (kmsTarget, error) {
	if (logGroup == "") == (resourceIdentifier == "") {
		return kmsTarget{}, errors.New(errors.InvalidArgument,
			"Exactly one of logGroupName or resourceIdentifier must be specified")
	}

	if logGroup == "" {
		switch {
		case strings.Contains(resourceIdentifier, queryResultARNPart):
			if !strings.HasSuffix(resourceIdentifier, queryResultARNPart+queryResultWildcard) {
				return kmsTarget{}, errors.Newf(errors.InvalidArgument,
					"Invalid query-result resource identifier %q", resourceIdentifier)
			}

			return kmsTarget{arn: resourceIdentifier}, nil
		case strings.Contains(resourceIdentifier, logGroupARNMarker):
			i := strings.LastIndex(resourceIdentifier, logGroupARNMarker)
			logGroup = strings.TrimSuffix(resourceIdentifier[i+len(logGroupARNMarker):], ":*")
		default:
			return kmsTarget{}, errors.Newf(errors.InvalidArgument,
				"Invalid resource identifier %q", resourceIdentifier)
		}
	}

	g, ok := m.groups.Get(logGroup)
	if !ok {
		return kmsTarget{}, errors.Newf(errors.NotFound, "log group %q not found", logGroup)
	}

	return kmsTarget{group: logGroup, arn: g.info.ResourceID}, nil
}

func (m *Mock) setKmsKey(t kmsTarget, keyID string) error {
	if t.group == "" {
		m.mu.Lock()
		m.queryResultKmsKey = keyID
		m.mu.Unlock()

		return nil
	}

	if !m.groups.Update(t.group, func(g *logGroup) *logGroup {
		g.kmsKeyID = keyID
		return g
	}) {
		return errors.Newf(errors.NotFound, "log group %q not found", t.group)
	}

	return nil
}

package kafka

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// BatchAssociateScramSecret associates a set of SCRAM secret ARNs with a
// cluster. A malformed secret ARN (not an arn:aws:secretsmanager: ARN) is
// reported as an unprocessed entry naming the ARN and why, rather than being
// stored; well-formed ARNs are added idempotently.
func (m *Mock) BatchAssociateScramSecret(
	_ context.Context, clusterARN string, secretARNs []string,
) ([]json.RawMessage, error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	var unprocessed []json.RawMessage

	for _, arn := range secretARNs {
		if bad := validateSecretARN(arn); bad != "" {
			unprocessed = append(unprocessed, unprocessedSecret(arn, "InvalidSecretArn", bad))

			continue
		}

		if containsString(cd.scramSecrets, arn) {
			continue
		}

		cd.scramSecrets = append(cd.scramSecrets, arn)
	}

	return unprocessed, nil
}

// BatchDisassociateScramSecret removes a set of SCRAM secret ARNs from a
// cluster. A secret not currently associated is reported as an unprocessed
// entry rather than silently ignored.
func (m *Mock) BatchDisassociateScramSecret(
	_ context.Context, clusterARN string, secretARNs []string,
) ([]json.RawMessage, error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	var unprocessed []json.RawMessage

	for _, arn := range secretARNs {
		if !containsString(cd.scramSecrets, arn) {
			unprocessed = append(unprocessed,
				unprocessedSecret(arn, "SecretNotAssociated", "secret is not associated with the cluster"))

			continue
		}

		cd.scramSecrets = removeString(cd.scramSecrets, arn)
	}

	return unprocessed, nil
}

// ListScramSecrets lists a cluster's associated SCRAM secret ARNs, sorted for
// deterministic paging.
func (m *Mock) ListScramSecrets(
	_ context.Context, clusterARN string, page driver.Page,
) (secrets []string, next string, err error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, "", err
	}

	cd.mu.RLock()
	all := copyStrings(cd.scramSecrets)
	cd.mu.RUnlock()

	sort.Strings(all)

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// validateSecretARN returns an error reason when arn is not a Secrets Manager
// secret ARN, or "" when it is well-formed.
func validateSecretARN(arn string) string {
	if !strings.HasPrefix(arn, "arn:aws:secretsmanager:") {
		return "secret ARN must be a Secrets Manager ARN"
	}

	return ""
}

// unprocessedSecret renders an UnprocessedScramSecret wire entry.
func unprocessedSecret(arn, code, msg string) json.RawMessage {
	b, err := json.Marshal(map[string]any{
		"secretArn":    arn,
		"errorCode":    code,
		"errorMessage": msg,
	})
	if err != nil {
		return json.RawMessage("{}")
	}

	return b
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}

	return false
}

func removeString(s []string, v string) []string {
	out := s[:0]

	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}

	return out
}

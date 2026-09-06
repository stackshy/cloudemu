// Package mwaa provides an in-memory mock implementation of the Amazon Managed
// Workflows for Apache Airflow (MWAA) control plane: environments plus resource
// tagging and the CLI/web login tokens. An environment is created immediately
// in the AVAILABLE state with a stable arn, webserverUrl, serviceRoleArn and
// createdAt; its configuration blocks (networkConfiguration,
// loggingConfiguration, airflowConfigurationOptions, the worker/scheduler
// sizing and the S3 code paths) are carried verbatim so a round-tripped
// environment reflects everything the caller sent. Running Apache Airflow and
// executing DAGs are out of scope: this is a control-plane-only surface.
package mwaa

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

// Compile-time check that Mock implements driver.MWAA.
var _ driver.MWAA = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 25

// arnMarker scopes an MWAA resource ARN.
const arnMarker = ":airflow:"

// kindEnvironment is the resource segment of an MWAA environment ARN.
const kindEnvironment = "environment"

// webserverHostBytes is the number of hash bytes rendered into the stable
// webserver hostname (12 hex characters).
const webserverHostBytes = 6

// Mock is an in-memory implementation of the Amazon MWAA control plane.
type Mock struct {
	envs *memstore.Store[driver.Environment]
	opts *config.Options
}

// New creates a new MWAA mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		envs: memstore.New[driver.Environment](),
		opts: opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// environmentARN mints the stable ARN reported for an environment.
func (m *Mock) environmentARN(name string) string {
	return idgen.AWSARN("airflow", m.opts.Region, m.opts.AccountID, kindEnvironment+"/"+name)
}

// serviceRoleARN mints the service-linked role ARN MWAA reports; it is derived
// only from the account id, so it is stable across reads.
func (m *Mock) serviceRoleARN() string {
	return idgen.AWSARN("iam", "", m.opts.AccountID,
		"role/aws-service-role/airflow.amazonaws.com/AWSServiceRoleForAmazonMWAA")
}

// webserverURL mints the stable Apache Airflow web-server hostname. Real MWAA
// uses a random host label; the emulator derives it deterministically from the
// environment identity so repeated reads never drift.
func (m *Mock) webserverURL(name string) string {
	sum := sha256.Sum256([]byte(m.opts.AccountID + ":" + m.opts.Region + ":" + name))

	return fmt.Sprintf("%x.%s.airflow.amazonaws.com", sum[:webserverHostBytes], m.opts.Region)
}

// logGroupARN mints the stable CloudWatch Logs log-group ARN for one Apache
// Airflow log type of an environment.
func (m *Mock) logGroupARN(name, suffix string) string {
	return idgen.AWSARN("logs", m.opts.Region, m.opts.AccountID,
		"log-group:airflow-"+name+"-"+suffix+":*")
}

// resourceNameFromARN extracts the environment name from an MWAA resource ARN
// of the form arn:aws:airflow:{region}:{acct}:environment/{name}.
func resourceNameFromARN(arn string) (kind, name string) {
	if !strings.Contains(arn, arnMarker) {
		return "", ""
	}

	tail := arn[strings.LastIndex(arn, ":")+1:]

	slash := strings.IndexByte(tail, '/')
	if slash < 0 {
		return "", ""
	}

	return tail[:slash], tail[slash+1:]
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyConfig(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

	return out
}

// copyEnv returns an alias-free copy of an environment so callers cannot mutate
// stored state through the result.
func copyEnv(e *driver.Environment) driver.Environment {
	out := *e
	out.Tags = copyTags(e.Tags)
	out.Config = copyConfig(e.Config)

	return out
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

// encodeToken encodes a numeric list offset as an opaque pagination token.
func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

// decodeToken decodes an opaque pagination token to a numeric offset. An empty
// or malformed token decodes to 0 (start from the beginning).
func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

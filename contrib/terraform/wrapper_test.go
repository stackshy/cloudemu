package terraform_test

import (
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestCloudemuTfWrapperIsIdempotent drives the cloudemu-tf wrapper (not raw
// terraform) through init → apply → plan → destroy against an in-process
// CloudEmu. The fixture carries only an empty `provider "aws" {}` block; the
// wrapper injects the endpoints, credentials and flags. A post-apply
// `plan -detailed-exitcode` of 0 proves the generated provider config both
// reaches CloudEmu and round-trips.
func TestCloudemuTfWrapperIsIdempotent(t *testing.T) {
	bin := tfBinary(t) // skips when neither tofu nor terraform is present
	wrapper := wrapperPath(t)

	srv := httptest.NewServer(awsserver.NewFromProvider(cloudemu.NewAWS()))
	defer srv.Close()

	work := copyFixture(t, "wrapper")

	env := append(os.Environ(),
		"CLOUDEMU_ENDPOINT="+srv.URL,
		"CLOUDEMU_TF_BIN="+bin,
	)

	run := func(args ...string) (int, string) {
		full := append([]string{wrapper, "-chdir=" + work}, args...)
		cmd := exec.Command("sh", full...) //nolint:gosec // test-controlled args
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out) + errString(err)
	}

	code, out := run("init")
	require.Equal(t, 0, code, "init failed:\n%s", out)

	code, out = run("apply", "-auto-approve")
	require.Equal(t, 0, code, "apply failed:\n%s", out)
	t.Cleanup(func() { _, _ = run("destroy", "-auto-approve") })

	// -detailed-exitcode: 0 = no changes, 2 = a diff (perpetual diff), 1 = error.
	code, out = run("plan", "-detailed-exitcode")
	require.Equal(t, 0, code, "plan after apply is not empty (exit %d):\n%s", code, out)

	code, out = run("destroy", "-auto-approve")
	require.Equal(t, 0, code, "destroy failed:\n%s", out)
}

// wrapperEndpointKeys maps each AWS handler package under server/aws to the
// Terraform AWS provider endpoints keys the wrapper must set for it. An empty
// list means the provider has no endpoints key for that service.
var wrapperEndpointKeys = map[string][]string{ //nolint:gochecknoglobals // static audit table
	"acm":                      {"acm"},
	"aoss":                     {"opensearchserverless"},
	"apigateway":               {"apigateway"},
	"apigatewayv2":             {"apigatewayv2"},
	"appflow":                  {"appflow"},
	"apprunner":                {"apprunner"},
	"appsync":                  {"appsync"},
	"aps":                      {"amp"},
	"athena":                   {"athena"},
	"backup":                   {"backup"},
	"batch":                    {"batch"},
	"bedrock":                  {"bedrock"},
	"bedrockagent":             {"bedrockagent"},
	"bedrockagentruntime":      {}, // runtime API, no provider key
	"cloudformation":           {"cloudformation"},
	"cloudfront":               {"cloudfront"},
	"cloudtrail":               {"cloudtrail"},
	"cloudwatch":               {"cloudwatch"},
	"cloudwatchlogs":           {"cloudwatchlogs"},
	"codeartifact":             {"codeartifact"},
	"cognito":                  {"cognitoidp"},
	"configservice":            {"configservice"},
	"costexplorer":             {"ce"},
	"dynamodb":                 {"dynamodb"},
	"ec2":                      {"ec2", "autoscaling"}, // the EC2 handler also serves Auto Scaling
	"ecr":                      {"ecr"},
	"ecs":                      {"ecs"},
	"efs":                      {"efs"},
	"eks":                      {"eks"},
	"elasticache":              {"elasticache"},
	"elbv2":                    {"elbv2"},
	"emr":                      {"emr"},
	"eventbridge":              {"eventbridge"},
	"eventbridgescheduler":     {"scheduler"},
	"fis":                      {"fis"},
	"globalaccelerator":        {"globalaccelerator"},
	"glue":                     {"glue"},
	"grafana":                  {"grafana"},
	"guardduty":                {"guardduty"},
	"healthlake":               {"healthlake"},
	"iam":                      {"iam"},
	"kafka":                    {"kafka"},
	"kendra":                   {"kendra"},
	"keyspaces":                {"keyspaces"},
	"kinesis":                  {"kinesis"},
	"kinesisvideo":             {"kinesisvideo"},
	"kms":                      {"kms"},
	"lambda":                   {"lambda"},
	"location":                 {"location"},
	"memorydb":                 {"memorydb"},
	"mq":                       {"mq"},
	"mwaa":                     {"mwaa"},
	"networkfirewall":          {"networkfirewall"},
	"opensearch":               {"opensearch"},
	"rds":                      {"rds"},
	"redshift":                 {"redshift"},
	"resourceexplorer2":        {"resourceexplorer2"},
	"resourcegroupstaggingapi": {"resourcegroupstaggingapi"},
	"route53":                  {"route53"},
	"route53resolver":          {"route53resolver"},
	"s3":                       {"s3"},
	"sagemaker":                {"sagemaker"},
	"savingsplans":             {}, // no provider key
	"secretsmanager":           {"secretsmanager"},
	"servicequotas":            {"servicequotas"},
	"sesv2":                    {"sesv2"},
	"sfn":                      {"sfn"},
	"sns":                      {"sns"},
	"sqs":                      {"sqs"},
	"ssm":                      {"ssm"},
	"sts":                      {"sts"},
	"timestreamwrite":          {"timestreamwrite"},
	"transfer":                 {"transfer"},
	"vpclattice":               {"vpclattice"},
	"wafv2":                    {"wafv2"},
}

// TestCloudemuTfWrapperCoversEveryHandler fails when server/aws/aws.go gains a
// handler the audit table does not know, or when the generated override is
// missing a key from the table (a missing key sends that service to real AWS).
func TestCloudemuTfWrapperCoversEveryHandler(t *testing.T) {
	for _, pkg := range registeredAWSHandlers(t) {
		_, ok := wrapperEndpointKeys[pkg]
		require.True(t, ok, "server/aws/%s is not in wrapperEndpointKeys; add its endpoints key to cloudemu-tf", pkg)
	}

	var want []string
	for _, keys := range wrapperEndpointKeys {
		want = append(want, keys...)
	}

	sort.Strings(want)

	require.Equal(t, want, generatedEndpointKeys(t), "cloudemu-tf endpoints block does not match the audit table")
}

// TestCloudemuTfWrapperRequestsAccountID guards the ARN fix: with
// skip_requesting_account_id the provider builds ARNs with an empty account.
func TestCloudemuTfWrapperRequestsAccountID(t *testing.T) {
	require.NotContains(t, generateOverride(t), "skip_requesting_account_id")
}

// registeredAWSHandlers lists the server/aws/<pkg> handler packages that
// server/aws/aws.go imports.
func registeredAWSHandlers(t *testing.T) []string {
	t.Helper()

	src := filepath.Join("..", "..", "server", "aws", "aws.go")
	f, err := parser.ParseFile(token.NewFileSet(), src, nil, parser.ImportsOnly)
	require.NoError(t, err)

	const prefix = "github.com/stackshy/cloudemu/v2/server/aws/"

	var pkgs []string

	for _, imp := range f.Imports {
		path, uerr := strconv.Unquote(imp.Path.Value)
		require.NoError(t, uerr)

		if rest, ok := strings.CutPrefix(path, prefix); ok && !strings.Contains(rest, "/") {
			pkgs = append(pkgs, rest)
		}
	}

	require.NotEmpty(t, pkgs, "no server/aws handler imports found in aws.go")

	return pkgs
}

// generateOverride runs the wrapper with a no-op binary so it only writes the
// provider override, and returns that file.
func generateOverride(t *testing.T) string {
	t.Helper()

	noop, err := exec.LookPath("true")
	require.NoError(t, err)

	dir := t.TempDir()
	cmd := exec.Command("sh", wrapperPath(t), "-chdir="+dir, "init") //nolint:gosec // test-controlled args
	cmd.Env = append(os.Environ(), "CLOUDEMU_TF_BIN="+noop, "CLOUDEMU_ENDPOINT=http://cloudemu.test:4566")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "wrapper failed:\n%s", out)

	b, err := os.ReadFile(filepath.Join(dir, "cloudemu_providers_override.tf"))
	require.NoError(t, err)

	return string(b)
}

// generatedEndpointKeys returns the sorted keys of the override's endpoints block.
func generatedEndpointKeys(t *testing.T) []string {
	t.Helper()

	block := regexp.MustCompile(`(?s)endpoints \{(.*?)\n  \}`).FindStringSubmatch(generateOverride(t))
	require.Len(t, block, 2, "no endpoints block in the generated override")

	line := regexp.MustCompile(`(?m)^\s*([a-z0-9]+)\s*=\s*"http://cloudemu\.test:4566"$`)

	var keys []string
	for _, m := range line.FindAllStringSubmatch(block[1], -1) {
		keys = append(keys, m[1])
	}

	sort.Strings(keys)

	return keys
}

func wrapperPath(t *testing.T) string {
	t.Helper()

	p, err := filepath.Abs("cloudemu-tf")
	require.NoError(t, err)

	if _, statErr := os.Stat(p); statErr != nil {
		t.Fatalf("wrapper script not found at %s: %v", p, statErr)
	}

	return p
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return "\n" + err.Error()
}

package aws

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// fullServer builds the AWS server with every handler registered.
func fullServer(t *testing.T) (*server.Server, authzSets) {
	t.Helper()

	d := DriversFrom(cloudemu.NewAWS())
	d.EnforceAuth = true
	d.K8sAPI = kubernetes.NewAPIServer()

	return newServer(d)
}

// TestEveryHandlerDeclaresAuthorization fails when a handler is registered
// without telling the gate how IAM authorizes it. Every handler must name its
// IAM service; a JSON-RPC handler's service must be in the target table; only
// the Kubernetes data plane is left to authentication alone.
func TestEveryHandlerDeclaresAuthorization(t *testing.T) {
	srv, sets := fullServer(t)

	tableServices := map[string]bool{}
	for _, svc := range jsonRPCServiceByTarget {
		tableServices[svc] = true
	}

	handlers := srv.Handlers()
	if len(handlers) < 70 {
		t.Fatalf("only %d handlers registered; the full server should have every service", len(handlers))
	}

	for _, h := range handlers {
		name := fmt.Sprintf("%T", h)
		svc := iamService(h)
		_, resolver := h.(awsauthz.Resolver)

		switch {
		case sets.authnOnly[h]:
			if name != "*kubernetes.APIServer" {
				t.Errorf("%s is authn-only; only the Kubernetes data plane may be", name)
			}
		case !servicePrefix.MatchString(svc):
			t.Errorf("%s declares no valid IAM service (%q)", name, svc)
		case sets.jsonRPC[h] && resolver:
			t.Errorf("%s is both a JSON-RPC handler and a Resolver", name)
		case sets.jsonRPC[h] && !tableServices[svc]:
			t.Errorf("JSON-RPC handler %s serves %q, which no X-Amz-Target prefix maps to", name, svc)
		}
	}

	if len(sets.authnOnly) != 1 {
		t.Errorf("authn-only handlers = %d, want 1 (Kubernetes)", len(sets.authnOnly))
	}
}

// TestHandlerIAMServicesMatchTable pins every handler's IAM service prefix,
// and checks the JSON-RPC target table routes each prefix to a handler that
// declares the same service.
func TestHandlerIAMServicesMatchTable(t *testing.T) {
	want := map[string]string{
		// Tier 1: op-level checks.
		"*iam.Handler": "iam", "*sts.Handler": "sts", "*rds.Handler": "rds", "*redshift.Handler": "redshift",
		"*elasticache.Handler": "elasticache", "*elbv2.Handler": "elasticloadbalancing", "*sns.Handler": "sns",
		"*cloudformation.Handler": "cloudformation", "*cloudwatch.Handler": "cloudwatch", "*ec2.Handler": "ec2",
		"*sagemaker.Handler": "sagemaker", "*s3.Handler": "s3", "*lambda.Handler": "lambda",
		"*route53.Handler": "route53", "*cloudfront.Handler": "cloudfront",
		// Tier 0: REST, service level.
		"*apigateway.Handler": "apigateway",
		"*apigatewayv2.Handler": "apigateway", "*eks.Handler": "eks",
		"*efs.Handler": "elasticfilesystem", "*batch.Handler": "batch",
		"*sesv2.Handler": "ses", "*opensearch.Handler": "es", "*appsync.Handler": "appsync", "*appflow.Handler": "appflow",
		"*mwaa.Handler": "airflow", "*mq.Handler": "mq", "*codeartifact.Handler": "codeartifact", "*backup.Handler": "backup",
		"*fis.Handler": "fis", "*grafana.Handler": "grafana", "*eventbridgescheduler.Handler": "scheduler",
		"*aps.Handler": "aps", "*kafka.Handler": "kafka", "*guardduty.Handler": "guardduty", "*bedrock.Handler": "bedrock",
		"*bedrockagent.Handler": "bedrock", "*bedrockagentruntime.Handler": "bedrock",
		"*resourceexplorer2.Handler": "resource-explorer-2", "*kinesisvideo.Handler": "kinesisvideo",
		"*location.Handler": "geo", "*vpclattice.Handler": "vpc-lattice", "*savingsplans.Handler": "savingsplans",
		// JSON-RPC.
		"*dynamodb.Handler": "dynamodb", "*dynamodb.StreamsHandler": "dynamodb", "*sqs.Handler": "sqs", "*ssm.Handler": "ssm",
		"*kms.Handler": "kms", "*acm.Handler": "acm", "*sfn.Handler": "states", "*kinesis.Handler": "kinesis",
		"*cloudtrail.Handler": "cloudtrail", "*glue.Handler": "glue", "*aoss.Handler": "aoss", "*kendra.Handler": "kendra",
		"*athena.Handler": "athena", "*cognito.Handler": "cognito-idp", "*cognito.WellKnown": "cognito-idp", "*configservice.Handler": "config",
		"*wafv2.Handler": "wafv2", "*ecs.Handler": "ecs", "*ecr.Handler": "ecr", "*route53resolver.Handler": "route53resolver",
		"*eventbridge.Handler": "events", "*cloudwatchlogs.Handler": "logs", "*secretsmanager.Handler": "secretsmanager",
		"*keyspaces.Handler": "cassandra", "*memorydb.Handler": "memorydb", "*networkfirewall.Handler": "network-firewall",
		"*resourcegroupstaggingapi.Handler": "tag", "*transfer.Handler": "transfer", "*timestreamwrite.Handler": "timestream",
		"*healthlake.Handler": "healthlake", "*apprunner.Handler": "apprunner", "*globalaccelerator.Handler": "globalaccelerator",
		"*costexplorer.Handler": "ce", "*servicequotas.Handler": "servicequotas", "*emr.Handler": "elasticmapreduce",
		// Kubernetes RBAC, not IAM.
		"*kubernetes.APIServer": "",
	}

	srv, sets := fullServer(t)

	for _, h := range srv.Handlers() {
		name := fmt.Sprintf("%T", h)

		svc, ok := want[name]
		if !ok {
			t.Errorf("%s is not in this table; add its IAM service prefix", name)
			continue
		}

		if got := iamService(h); got != svc {
			t.Errorf("%s IAMService = %q, want %q", name, got, svc)
		}
	}

	matched := map[server.Handler]bool{}

	for prefix, svc := range jsonRPCServiceByTarget {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		req.Header.Set("X-Amz-Target", prefix+"Probe")
		req.Header.Set("Content-Type", amzJSON11)

		h := srv.Match(req)
		if h == nil {
			t.Errorf("target prefix %s routes to no handler", prefix)
			continue
		}

		_, resolver := h.(awsauthz.Resolver)
		if !sets.jsonRPC[h] && !resolver {
			t.Errorf("target prefix %s routes to %T, which is neither a JSON-RPC handler nor a Resolver", prefix, h)
		}

		if got := iamService(h); got != svc {
			t.Errorf("target prefix %s maps to %q but routes to %T serving %q", prefix, svc, h, got)
		}

		matched[h] = true
	}

	for h := range sets.jsonRPC {
		if !matched[h] {
			t.Errorf("JSON-RPC handler %T has no X-Amz-Target prefix in the table", h)
		}
	}
}

// TestOpLevelRESTHandlersAreResolvers pins the REST handlers that name the IAM
// action and resource of each operation, so none falls back to service-level
// authorization unnoticed.
func TestOpLevelRESTHandlersAreResolvers(t *testing.T) {
	want := map[string]bool{
		"*s3.Handler": true, "*lambda.Handler": true, "*route53.Handler": true, "*cloudfront.Handler": true,
	}

	srv, _ := fullServer(t)

	for _, h := range srv.Handlers() {
		name := fmt.Sprintf("%T", h)
		if !want[name] {
			continue
		}

		delete(want, name)

		if _, ok := h.(awsauthz.Resolver); !ok {
			t.Errorf("%s is not a Resolver", name)
		}
	}

	for name := range want {
		t.Errorf("%s is not registered", name)
	}
}

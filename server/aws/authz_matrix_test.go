package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/fxamacker/cbor/v2"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// sreq is one signed request in the authorization matrix.
type sreq struct {
	method, path, ctype, body, service, host string
	header                                   map[string]string
}

func doSigned(t *testing.T, ts *httptest.Server, creds aws.Credentials, rq sreq) (int, string) {
	t.Helper()

	ctx := context.Background()

	method := rq.method
	if method == "" {
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, ts.URL+rq.path, strings.NewReader(rq.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if rq.host != "" {
		req.Host = rq.host
	}

	if rq.ctype != "" {
		req.Header.Set("Content-Type", rq.ctype)
	}

	for k, v := range rq.header {
		req.Header.Set(k, v)
	}

	sum := sha256.Sum256([]byte(rq.body))
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), rq.service, "us-east-1", time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

func form(service, body string) sreq {
	return sreq{path: "/", ctype: formCT, body: body, service: service}
}

func allow(actions ...string) string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["` +
		strings.Join(actions, `","`) + `"],"Resource":"*"}]}`
}

const (
	allowAllDenyBucket = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"},` +
		`{"Effect":"Deny","Action":"s3:DeleteBucket","Resource":"arn:aws:s3:::prod"}]}`
	xmlAccessDenied = "<Code>AccessDenied</Code>"
	createEvilUser  = "Action=CreateUser&Version=2010-05-08&UserName=evil"
	lambdaCreate    = `{"FunctionName":"f1","Runtime":"python3.12","Role":"arn:aws:iam::123456789012:role/r",` +
		`"Handler":"index.handler","Code":{"ZipFile":"UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="}}`
)

// matrixServer is the full AWS wire server under --enforce-auth, plus the
// shared Kubernetes data plane so the authn-only plan is reachable.
func matrixServer(t *testing.T, mutate func(*Drivers)) (*httptest.Server, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	d := DriversFrom(cloud)
	d.EnforceAuth = true
	d.K8sAPI = kubernetes.NewAPIServer()

	if mutate != nil {
		mutate(&d)
	}

	ts := httptest.NewServer(New(d))
	t.Cleanup(ts.Close)

	return ts, cloud
}

func userCount(t *testing.T, cloud *awsprovider.Provider, name string) int {
	t.Helper()

	users, err := cloud.IAM.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	n := 0

	for _, u := range users {
		if u.Name == name {
			n++
		}
	}

	return n
}

func bucketExists(t *testing.T, cloud *awsprovider.Provider, name string) bool {
	t.Helper()

	buckets, err := cloud.S3.ListBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	for _, b := range buckets {
		if b.Name == name {
			return true
		}
	}

	return false
}

func wantDenied(t *testing.T, status int, body, shape string) {
	t.Helper()

	if status != http.StatusForbidden || !strings.Contains(body, shape) {
		t.Fatalf("status %d, body %s; want 403 with %s", status, body, shape)
	}
}

func wantNotDenied(t *testing.T, status int, body string) {
	t.Helper()

	if status == http.StatusForbidden {
		t.Fatalf("denied: %s", body)
	}
}

// TestAuthzMatrixQuery covers the query protocol: the IAM action comes from the
// handler that dispatch picks and the form Action it reads, never from the
// SigV4 scope.
func TestAuthzMatrixQuery(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	dyn := userWithPolicy(t, cloud, "dynonly", allowDynamo)
	lister := userWithPolicy(t, cloud, "lister", allow("iam:ListUsers"))

	t.Run("iam CreateUser by a dynamodb-only user", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, form("iam", createEvilUser))
		wantDenied(t, status, body, xmlAccessDenied)
	})

	t.Run("iam CreateUser signed with a dynamodb scope", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, form("dynamodb", createEvilUser))
		wantDenied(t, status, body, xmlAccessDenied)
	})

	t.Run("GET ?Action=CreateUser is ec2:CreateUser", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, sreq{method: http.MethodGet, path: "/?" + createEvilUser, service: "iam"})
		wantDenied(t, status, body, "ec2:CreateUser")
	})

	t.Run("duplicate Action: the first value is authorized and run", func(t *testing.T) {
		status, body := doSigned(t, ts, lister, form("iam", "Action=ListUsers&Action=CreateUser&Version=2010-05-08&UserName=evil"))
		if status != http.StatusOK {
			t.Fatalf("ListUsers-first: %d %s", status, body)
		}

		status, body = doSigned(t, ts, lister, form("iam", "Action=CreateUser&Action=ListUsers&Version=2010-05-08&UserName=evil"))
		wantDenied(t, status, body, "iam:CreateUser")
	})

	t.Run("body Action wins over the query-string Action", func(t *testing.T) {
		rq := form("iam", createEvilUser)
		rq.path = "/?Action=ListUsers"
		status, body := doSigned(t, ts, lister, rq)
		wantDenied(t, status, body, "iam:CreateUser")
	})

	t.Run("lower-case Action falls through to EC2", func(t *testing.T) {
		status, body := doSigned(t, ts, lister, form("iam", "Action=createuser&Version=2010-05-08&UserName=evil"))
		wantDenied(t, status, body, "ec2:createuser")
	})

	t.Run("unparseable body", func(t *testing.T) {
		status, body := doSigned(t, ts, lister, form("iam", "Action=CreateUser&UserName=evil&x=%zz"))
		if status < http.StatusBadRequest {
			t.Fatalf("unparseable body served: %d %s", status, body)
		}
	})

	if n := userCount(t, cloud, "evil"); n != 0 {
		t.Fatalf("a denied request created user evil")
	}

	t.Run("ListTagsForResource with an sns scope is sns", func(t *testing.T) {
		rds := userWithPolicy(t, cloud, "rdsonly", allow("rds:*"))
		status, body := doSigned(t, ts, rds, form("sns", "Action=ListTagsForResource&ResourceArn=arn:aws:sns:us-east-1:123456789012:t"))
		wantDenied(t, status, body, "sns:ListTagsForResource")
	})

	t.Run("autoscaling DeletePolicy is served by IAM", func(t *testing.T) {
		as := userWithPolicy(t, cloud, "asonly", allow("autoscaling:*"))
		status, body := doSigned(t, ts, as, form("autoscaling", "Action=DeletePolicy&PolicyName=p&AutoScalingGroupName=g"))
		wantDenied(t, status, body, "iam:DeletePolicy")
	})

	t.Run("autoscaling CreateAutoScalingGroup needs autoscaling", func(t *testing.T) {
		ec2u := userWithPolicy(t, cloud, "ec2only", allow("ec2:*"))
		status, body := doSigned(t, ts, ec2u, form("autoscaling",
			"Action=CreateAutoScalingGroup&AutoScalingGroupName=g1&MinSize=0&MaxSize=1&LaunchConfigurationName=lc"))
		wantDenied(t, status, body, "autoscaling:CreateAutoScalingGroup")

		groups, err := cloud.EC2.ListAutoScalingGroups(context.Background())
		if err != nil || len(groups) != 0 {
			t.Fatalf("groups %v err %v, want none", groups, err)
		}
	})

	t.Run("query-form SQS is ec2 and never reaches SQS", func(t *testing.T) {
		sq := userWithPolicy(t, cloud, "sqsonly", allowSQS)
		status, body := doSigned(t, ts, sq, form("sqs", "Action=CreateQueue&QueueName=q1"))
		wantDenied(t, status, body, "ec2:CreateQueue")

		qs, _ := cloud.SQS.ListQueues(context.Background(), "")
		if len(qs) != 0 {
			t.Fatalf("queues %v, want none", qs)
		}
	})

	t.Run("cloudwatch query PutMetricData with a fine-grained policy", func(t *testing.T) {
		cw := userWithPolicy(t, cloud, "cwput", allow("cloudwatch:PutMetricData"))
		status, body := doSigned(t, ts, cw, form("monitoring",
			"Action=PutMetricData&Version=2010-08-01&Namespace=App&MetricData.member.1.MetricName=Hits&MetricData.member.1.Value=1"))
		if status != http.StatusOK {
			t.Fatalf("PutMetricData: %d %s", status, body)
		}
	})
}

// TestAuthzMatrixProtocolMixing sends one protocol's operation with another
// protocol's operation marker. The gate must authorize what dispatch runs.
func TestAuthzMatrixProtocolMixing(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	describer := userWithPolicy(t, cloud, "describer", allow("cloudwatch:DescribeAlarms"))

	payload, err := cbor.Marshal(map[string]any{
		"Namespace": "Mixed", "MetricData": []any{map[string]any{"MetricName": "M", "Value": 1.0}},
	})
	if err != nil {
		t.Fatalf("cbor: %v", err)
	}

	t.Run("cbor PutMetricData with ?Action=DescribeAlarms", func(t *testing.T) {
		status, body := doSigned(t, ts, describer, sreq{
			path:  "/service/GraniteServiceVersion20100801/operation/PutMetricData?Action=DescribeAlarms",
			ctype: "application/cbor", body: string(payload), service: "monitoring",
			header: map[string]string{"Smithy-Protocol": "rpc-v2-cbor"},
		})
		if status != http.StatusForbidden {
			t.Fatalf("status %d %q, want 403", status, body)
		}
	})

	t.Run("json 1.0 PutMetricData with ?Action=DescribeAlarms", func(t *testing.T) {
		status, body := doSigned(t, ts, describer, sreq{
			path: "/?Action=DescribeAlarms", ctype: "application/x-amz-json-1.0",
			body: `{"Namespace":"Mixed","MetricData":[{"MetricName":"M","Value":1}]}`, service: "monitoring",
			header: map[string]string{"X-Amz-Target": "GraniteServiceVersion20100801.PutMetricData"},
		})
		wantDenied(t, status, body, accessDeny)
	})

	if names, _ := cloud.CloudWatch.ListMetrics(context.Background(), "Mixed"); len(names) != 0 {
		t.Fatalf("a denied PutMetricData stored %v", names)
	}

	t.Run("json 1.0 DescribeAlarms is allowed", func(t *testing.T) {
		status, body := doSigned(t, ts, describer, sreq{
			path: "/", ctype: "application/x-amz-json-1.0", body: `{}`, service: "monitoring",
			header: map[string]string{"X-Amz-Target": "GraniteServiceVersion20100801.DescribeAlarms"},
		})
		if status != http.StatusOK {
			t.Fatalf("DescribeAlarms: %d %s", status, body)
		}
	})
}

// TestAuthzMatrixForgedTarget sends REST and query requests carrying a forged
// X-Amz-Target from a caller allowed only dynamodb:PutItem. The header must not
// steer the authorized action away from the handler that runs.
func TestAuthzMatrixForgedTarget(t *testing.T) {
	forged := map[string]string{"X-Amz-Target": "DynamoDB_20120810.PutItem"}

	t.Run("s3, iam and rds keep their state", func(t *testing.T) {
		ts, cloud := matrixServer(t, nil)
		put := userWithPolicy(t, cloud, "putonly", allow("dynamodb:PutItem"))

		cases := []sreq{
			{method: http.MethodPut, path: "/forged-bucket", service: "s3", header: forged},
			{path: "/", ctype: formCT, body: createEvilUser, service: "iam", header: forged},
			{path: "/", ctype: formCT, body: "Action=CreateDBSubnetGroup&DBSubnetGroupName=g&DBSubnetGroupDescription=d" +
				"&SubnetIds.member.1=subnet-1", service: "rds", header: forged},
		}

		for _, rq := range cases {
			if status, body := doSigned(t, ts, put, rq); status < http.StatusBadRequest {
				t.Fatalf("%s %s: forged target served %d %s", rq.method, rq.path, status, body)
			}
		}

		if bucketExists(t, cloud, "forged-bucket") || userCount(t, cloud, "evil") != 0 {
			t.Fatal("a forged-target request changed S3 or IAM state")
		}
	})

	t.Run("a REST handler that ignores the header", func(t *testing.T) {
		// Without DynamoDB registered, the forged target reaches Lambda, which
		// routes on its path. It must be authorized as lambda, not dynamodb.
		ts, cloud := matrixServer(t, func(d *Drivers) { d.DynamoDB = nil })
		put := userWithPolicy(t, cloud, "putonly", allow("dynamodb:PutItem"))

		status, body := doSigned(t, ts, put, sreq{
			path: lambdaPath, ctype: "application/json", body: lambdaCreate, service: "lambda", header: forged,
		})
		wantDenied(t, status, body, accessDeny)

		if fns, _ := cloud.Lambda.ListFunctions(context.Background()); len(fns) != 0 {
			t.Fatalf("functions %v, want none", fns)
		}
	})
}

// TestAuthzMatrixJSONRPCPrefixes checks the JSON-RPC services that had no
// entry in the target table are usable by an unrestricted caller.
func TestAuthzMatrixJSONRPCPrefixes(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	boot := userWithPolicy(t, cloud, "boot", "")

	for _, tc := range []struct{ service, target string }{
		{"ce", "AWSInsightsIndexService.GetCostAndUsage"},
		{"servicequotas", "ServiceQuotasV20190624.ListServices"},
		{"elasticmapreduce", "ElasticMapReduce.ListClusters"},
	} {
		t.Run(tc.service, func(t *testing.T) {
			if status, typ := signedJSONRPC(t, ts, boot, tc.service, tc.target); status == http.StatusForbidden {
				t.Fatalf("%s denied: %s", tc.target, typ)
			}
		})
	}
}

// TestAuthzMatrixREST covers REST services, which stay service-level until
// each moves to op-level checks.
func TestAuthzMatrixREST(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	dyn := userWithPolicy(t, cloud, "dynonly", allowDynamo)

	t.Run("s3 mb by a dynamodb-only user", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, sreq{method: http.MethodPut, path: "/dyn-bucket", service: "s3"})
		wantDenied(t, status, body, "<Error><Code>AccessDenied</Code>")

		if bucketExists(t, cloud, "dyn-bucket") {
			t.Fatal("bucket created")
		}
	})

	t.Run("s3 request signed with a dynamodb scope", func(t *testing.T) {
		_, _ = doSigned(t, ts, dyn, sreq{method: http.MethodPut, path: "/scoped-bucket", service: "dynamodb"})

		if bucketExists(t, cloud, "scoped-bucket") {
			t.Fatal("bucket created")
		}
	})

	for _, tc := range []struct {
		name string
		rq   sreq
	}{
		{"lambda CreateFunction", sreq{path: lambdaPath, ctype: "application/json", body: lambdaCreate, service: "lambda"}},
		{"apigateway CreateRestApi", sreq{path: "/restapis", ctype: "application/json", body: `{"name":"a"}`, service: "apigateway"}},
		{"eks CreateCluster", sreq{path: "/clusters", ctype: "application/json", service: "eks",
			body: `{"name":"c1","roleArn":"arn:aws:iam::123456789012:role/r","resourcesVpcConfig":{}}`}},
	} {
		t.Run(tc.name+" by a dynamodb-only user", func(t *testing.T) {
			status, body := doSigned(t, ts, dyn, tc.rq)
			wantDenied(t, status, body, accessDeny)
		})
	}

	t.Run("service-wide grant is allowed", func(t *testing.T) {
		s3all := userWithPolicy(t, cloud, "s3all", allow("s3:*"))
		status, body := doSigned(t, ts, s3all, sreq{method: http.MethodPut, path: "/wide-bucket", service: "s3"})
		wantNotDenied(t, status, body)

		if !bucketExists(t, cloud, "wide-bucket") {
			t.Fatal("bucket not created")
		}
	})

	t.Run("allow-all with one deny on the service", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "denyone", allowAllDenyBucket)
		status, body := doSigned(t, ts, u, sreq{method: http.MethodPut, path: "/denyone-bucket", service: "s3"})
		wantDenied(t, status, body, xmlAccessDenied)
	})

	t.Run("unknown REST path is 501", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, sreq{method: http.MethodGet, path: "/", service: "dynamodb",
			header: map[string]string{"X-Amz-Target": "NoSuchService_2020.Op"}})
		if status != http.StatusNotImplemented {
			t.Fatalf("status %d %s, want 501", status, body)
		}
	})

	t.Run("kubernetes data plane is authn-only", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, sreq{method: http.MethodGet, path: "/k8s/none/api", service: "eks"})
		wantNotDenied(t, status, body)
	})
}

// TestAuthzMatrixSTS covers the basic STS checks.
func TestAuthzMatrixSTS(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()
	dyn := userWithPolicy(t, cloud, "dynonly", allowDynamo)

	trust := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::` + defaultTestAccount +
		`:root"},"Action":"sts:AssumeRole"}]}`
	if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: "target", AssumeRolePolicyDoc: trust}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	roleArn := "arn:aws:iam::" + defaultTestAccount + ":role/target"
	assume := "Action=AssumeRole&Version=2011-06-15&RoleSessionName=s&RoleArn=" + roleArn

	t.Run("AssumeRole without sts:AssumeRole", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, form("sts", assume))
		wantDenied(t, status, body, "sts:AssumeRole")
	})

	t.Run("AssumeRole with sts:AssumeRole on the role", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "assumer", `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole","Resource":"`+roleArn+`"}]}`)
		status, body := doSigned(t, ts, u, form("sts", assume))
		if status != http.StatusOK {
			t.Fatalf("AssumeRole: %d %s", status, body)
		}
	})

	t.Run("the deny message does not reveal whether the role exists", func(t *testing.T) {
		if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{
			Name: "pathed", Path: "/team/", AssumeRolePolicyDoc: trust,
		}); err != nil {
			t.Fatalf("CreateRole: %v", err)
		}

		send := func(name string) string {
			_, body := doSigned(t, ts, dyn, form("sts", "Action=AssumeRole&Version=2011-06-15&RoleSessionName=s"+
				"&RoleArn=arn:aws:iam::"+defaultTestAccount+":role/"+name))

			return body[strings.Index(body, "<Message>"):strings.Index(body, "</Message>")]
		}

		existing, missing := send("pathed"), send("absent")
		if strings.ReplaceAll(missing, "role/absent", "role/pathed") != existing {
			t.Fatalf("messages differ:\n existing: %s\n missing:  %s", existing, missing)
		}

		if !strings.HasSuffix(existing, "on resource: arn:aws:iam::"+defaultTestAccount+":role/pathed") {
			t.Fatalf("the message must name the RoleArn as sent: %s", existing)
		}
	})

	t.Run("GetCallerIdentity needs no permission", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, form("sts", "Action=GetCallerIdentity&Version=2011-06-15"))
		if status != http.StatusOK {
			t.Fatalf("GetCallerIdentity: %d %s", status, body)
		}
	})

	t.Run("GetSessionToken is only blocked by an explicit deny", func(t *testing.T) {
		status, body := doSigned(t, ts, dyn, form("sts", "Action=GetSessionToken&Version=2011-06-15"))
		if status != http.StatusOK {
			t.Fatalf("GetSessionToken: %d %s", status, body)
		}

		denied := userWithPolicy(t, cloud, "nosession", `{"Statement":[{"Effect":"Allow","Action":"*","Resource":"*"},`+
			`{"Effect":"Deny","Action":"sts:GetSessionToken","Resource":"*"}]}`)
		status, body = doSigned(t, ts, denied, form("sts", "Action=GetSessionToken&Version=2011-06-15"))
		wantDenied(t, status, body, xmlAccessDenied)
	})
}

// TestAuthzMatrixSTSTrust covers the AssumeRole trust rows: the trust policy
// is evaluated for the signed caller, and each kind of temporary credential
// is limited to the STS and IAM calls AWS allows it.
func TestAuthzMatrixSTSTrust(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()
	acct := "arn:aws:iam::" + defaultTestAccount
	assumer := userWithPolicy(t, cloud, "assumer", allow("sts:AssumeRole"))
	dyn := userWithPolicy(t, cloud, "dyn", allowDynamo)
	boot := userWithPolicy(t, cloud, "boot", "")

	roles := map[string]string{
		"rootonly": `{"AWS":"` + acct + `:root"}`,
		"dynnamed": `{"AWS":"` + acct + `:user/dyn"}`,
		"otheruser": `{"AWS":"` + acct + `:user/someone"}`,
		"federated": `{"Federated":"*"}`,
	}
	for name, principal := range roles {
		trust := `{"Statement":[{"Effect":"Allow","Principal":` + principal + `,"Action":"sts:AssumeRole"}]}`
		if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: name, AssumeRolePolicyDoc: trust}); err != nil {
			t.Fatalf("CreateRole: %v", err)
		}
	}

	ext := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"` + acct + `:user/dyn"},"Action":"sts:AssumeRole",` +
		`"Condition":{"StringEquals":{"sts:ExternalId":"e1"}}}]}`
	if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: "external", AssumeRolePolicyDoc: ext}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	assume := func(creds aws.Credentials, role, extra string) (int, string) {
		return doSigned(t, ts, creds, form("sts", "Action=AssumeRole&Version=2011-06-15&RoleSessionName=s&RoleArn="+
			acct+":role/"+role+extra))
	}

	cases := []struct {
		name    string
		creds   aws.Credentials
		role    string
		extra   string
		allowed bool
	}{
		{"permission but trust mismatch", assumer, "otheruser", "", false},
		{"trust names the user, no identity policy", dyn, "dynnamed", "", true},
		{"trust names root, no identity policy", dyn, "rootonly", "", false},
		{"trust names root, identity allow", assumer, "rootonly", "", true},
		{"ExternalId matches", dyn, "external", "&ExternalId=e1", true},
		{"ExternalId differs", dyn, "external", "&ExternalId=e2", false},
		{"Federated star does not match an IAM user", assumer, "federated", "", false},
		{"unrestricted caller, trust names root", boot, "rootonly", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := assume(tc.creds, tc.role, tc.extra)
			if tc.allowed {
				if status != http.StatusOK {
					t.Fatalf("AssumeRole: %d %s", status, body)
				}

				return
			}

			wantDenied(t, status, body, xmlAccessDenied)
		})
	}

	t.Run("foreign account and wrong path are refused", func(t *testing.T) {
		for _, arn := range []string{"arn:aws:iam::999999999999:role/rootonly", acct + ":role/x/rootonly"} {
			status, body := doSigned(t, ts, boot, form("sts", "Action=AssumeRole&Version=2011-06-15&RoleSessionName=s&RoleArn="+arn))
			wantDenied(t, status, body, xmlAccessDenied)
		}
	})

	session := func(action string) aws.Credentials {
		status, body := doSigned(t, ts, boot, form("sts", "Action="+action+"&Version=2011-06-15&Name=fed"))
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", action, status, body)
		}

		return aws.Credentials{
			AccessKeyID:     between(body, "<AccessKeyId>", "</AccessKeyId>"),
			SecretAccessKey: between(body, "<SecretAccessKey>", "</SecretAccessKey>"),
			SessionToken:    between(body, "<SessionToken>", "</SessionToken>"),
		}
	}

	federation, sessionToken := session("GetFederationToken"), session("GetSessionToken")

	t.Run("federation credentials cannot AssumeRole", func(t *testing.T) {
		status, body := assume(federation, "rootonly", "")
		wantDenied(t, status, body, xmlAccessDenied)
	})

	t.Run("session-token credentials cannot call IAM", func(t *testing.T) {
		status, body := doSigned(t, ts, sessionToken, form("iam", "Action=ListUsers&Version=2010-05-08"))
		wantDenied(t, status, body, xmlAccessDenied)
	})

	t.Run("role chaining when the trust allows it", func(t *testing.T) {
		status, body := assume(boot, "rootonly", "")
		if status != http.StatusOK {
			t.Fatalf("AssumeRole: %d %s", status, body)
		}

		chain := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"` + acct + `:role/rootonly"},"Action":"sts:AssumeRole"}]}`
		if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: "next", AssumeRolePolicyDoc: chain}); err != nil {
			t.Fatalf("CreateRole: %v", err)
		}

		roleCreds := aws.Credentials{
			AccessKeyID:     between(body, "<AccessKeyId>", "</AccessKeyId>"),
			SecretAccessKey: between(body, "<SecretAccessKey>", "</SecretAccessKey>"),
			SessionToken:    between(body, "<SessionToken>", "</SessionToken>"),
		}

		if status, body := assume(roleCreds, "next", ""); status != http.StatusOK {
			t.Fatalf("chained AssumeRole: %d %s", status, body)
		}
	})

	t.Run("signed web identity and SAML are refused", func(t *testing.T) {
		for _, q := range []string{
			"Action=AssumeRoleWithWebIdentity&Version=2011-06-15&RoleSessionName=s&WebIdentityToken=junk&RoleArn=" + acct + ":role/federated",
			"Action=AssumeRoleWithSAML&Version=2011-06-15&PrincipalArn=p&SAMLAssertion=eA%3D%3D&RoleArn=" + acct + ":role/federated",
		} {
			status, body := doSigned(t, ts, boot, form("sts", q))
			wantDenied(t, status, body, "not available under --enforce-auth")
		}
	})

	t.Run("GetCallerIdentity works for every principal", func(t *testing.T) {
		for name, creds := range map[string]aws.Credentials{
			"user": dyn, "boot": boot, "federation": federation, "session token": sessionToken,
		} {
			if status, body := doSigned(t, ts, creds, form("sts", "Action=GetCallerIdentity&Version=2011-06-15")); status != http.StatusOK {
				t.Fatalf("%s: %d %s", name, status, body)
			}
		}
	})
}

// between returns the text of s between the first open and the next close.
func between(s, open, closing string) string {
	_, rest, _ := strings.Cut(s, open)
	v, _, _ := strings.Cut(rest, closing)

	return v
}

// TestAuthzMatrixBootstrap checks the shortcut principals are unrestricted on
// every plan except an unmapped JSON-RPC target.
func TestAuthzMatrixBootstrap(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	boot := userWithPolicy(t, cloud, "boot", "")
	root := userWithPolicy(t, cloud, "root", allowDynamo)

	for name, creds := range map[string]aws.Credentials{"boot": boot, "root": root} {
		t.Run(name, func(t *testing.T) {
			for _, rq := range []sreq{
				form("iam", "Action=CreateUser&Version=2010-05-08&UserName=made-by-"+name),
				{method: http.MethodPut, path: "/" + name + "-bucket", service: "s3"},
				form("ec2", "Action=DescribeInstances&Version=2016-11-15"),
				form("sts", "Action=GetCallerIdentity&Version=2011-06-15"),
				{path: lambdaPath, ctype: "application/json", body: strings.Replace(lambdaCreate, "f1", "fn-"+name, 1), service: "lambda"},
				{method: http.MethodGet, path: "/k8s/none/api", service: "eks"},
			} {
				if status, body := doSigned(t, ts, creds, rq); status == http.StatusForbidden {
					t.Fatalf("%s %s denied: %s", rq.method, rq.path, body)
				}
			}

			status, _ := signedJSONRPC(t, ts, creds, "cloudtrail", "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101.DescribeTrails")
			if status != http.StatusForbidden {
				t.Fatalf("unmapped JSON-RPC target: status %d, want 403", status)
			}
		})
	}
}

// TestAuthzMatrixTruncatingPeek covers a Matches that peeks at the body. The
// gate must authorize the bytes dispatch reads: Kinesis Video peeks at
// /TagResource bodies, and if it handed back only the first 64 KiB, EC2
// would run the query-string CreateVpc while the gate authorized the
// body's DescribeVpcs.
func TestAuthzMatrixTruncatingPeek(t *testing.T) {
	ts, cloud := matrixServer(t, func(d *Drivers) { d.SavingsPlans = false })
	viewer := userWithPolicy(t, cloud, "vpcviewer", allow("ec2:DescribeVpcs"))

	before, err := cloud.VPC.DescribeVPCs(context.Background(), nil)
	if err != nil {
		t.Fatalf("DescribeVPCs: %v", err)
	}

	status, body := doSigned(t, ts, viewer, sreq{
		path: "/TagResource?Action=CreateVpc&CidrBlock=10.9.0.0/16", ctype: "Application/x-www-form-urlencoded",
		body: "Pad=" + strings.Repeat("a", 70000) + "&Action=DescribeVpcs", service: "ec2",
	})

	after, err := cloud.VPC.DescribeVPCs(context.Background(), nil)
	if err != nil {
		t.Fatalf("DescribeVPCs: %v", err)
	}

	if len(after) != len(before) {
		t.Fatalf("a VPC was created (%d -> %d); response %d %.200s", len(before), len(after), status, body)
	}
}

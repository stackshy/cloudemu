package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// k8sDurabilityPorts is one run's endpoints plus its persistent state file, with
// the shared Kubernetes data-plane port enabled (the plain durability harness
// disables it).
type k8sDurabilityPorts struct {
	aws, k8s, state string
}

// startServeK8s launches `cloudemu serve` with the AWS provider and the shared
// Kubernetes data plane enabled, under an always-on persistence strategy. The
// caller must terminate the process.
func startServeK8s(t *testing.T, bin string, p k8sDurabilityPorts, strategy, interval string, extra ...string) *exec.Cmd {
	t.Helper()

	args := []string{
		"serve",
		"--host", "127.0.0.1",
		"--providers", "aws",
		"--aws-port", p.aws,
		"--k8s-port", p.k8s,
		"--quiet",
		"--persist",
		"--state-file", p.state,
		"--persist-strategy", strategy,
		"--persist-interval", interval,
	}
	args = append(args, extra...)

	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}

	if !waitReady("http://127.0.0.1:"+p.aws+"/", 15*time.Second) {
		_ = cmd.Process.Kill()
		t.Fatal("AWS endpoint never became ready")
	}

	return cmd
}

// insecureK8sClient dials the emulator's self-signed HTTPS data plane, skipping
// verification (the data plane serves an eksprov-generated cert; a real client
// would trust the advertised CA — here we only care about payload durability).
func insecureK8sClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed emulator cert
	}}
}

func k8sDo(t *testing.T, c *http.Client, method, url, body string) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, b
}

func newEKSClientMain(t *testing.T, baseURL string) *awseks.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")))
	if err != nil {
		t.Fatalf("awsconfig: %v", err)
	}

	return awseks.NewFromConfig(cfg, func(o *awseks.Options) {
		o.BaseEndpoint = aws.String(baseURL)
	})
}

const persistDeploymentBody = `{"apiVersion":"apps/v1","kind":"Deployment",` +
	`"metadata":{"name":"persist-deploy","namespace":"default"},` +
	`"spec":{"replicas":2,"selector":{"matchLabels":{"app":"persist"}},` +
	`"template":{"metadata":{"labels":{"app":"persist"}},` +
	`"spec":{"containers":[{"name":"web","image":"nginx:1.27"}]}}}}`

const persistServiceBody = `{"apiVersion":"v1","kind":"Service",` +
	`"metadata":{"name":"persist-svc","namespace":"default"},` +
	`"spec":{"selector":{"app":"persist"},"ports":[{"port":80,"targetPort":80}]}}`

// describeK8sEndpoint creates (idempotently) and describes an EKS cluster,
// returning its data-plane endpoint (…/k8s/<uid>).
func describeK8sEndpoint(t *testing.T, eks *awseks.Client, name string, create bool) string {
	t.Helper()

	ctx := context.Background()

	if create {
		if _, err := eks.CreateCluster(ctx, &awseks.CreateClusterInput{
			Name:               aws.String(name),
			RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
			ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-a", "subnet-b"}},
		}); err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
	}

	out, err := eks.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	return aws.ToString(out.Cluster.Endpoint)
}

// TestK8sDataPlanePersistsAcrossCrash is the #868 acceptance test: an EKS cluster
// created via the real SDK, with a Deployment + Service applied over its
// kubeconfig endpoint, survives a SIGKILL (no graceful shutdown) and a restart —
// (a) DescribeCluster returns the SAME /k8s/<uid>, (b) the OLD endpoint still
// serves, (c) the Deployment/Pods/Service/Endpoints survive with identities
// intact. It runs under BOTH the `scheduled` (default) and `on-request`
// strategies, so it proves the pure-kubectl dirty seam under each (no provider
// call ever mutates the data plane): under on-request a k8s-only mutation must
// still fire a save within the debounce cap.
func TestK8sDataPlanePersistsAcrossCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a binary; skipped in -short")
	}

	bin := buildServeBinary(t)

	for _, strategy := range []string{"scheduled", "on-request"} {
		t.Run(strategy, func(t *testing.T) {
			k8sDataPlaneCrashCase(t, bin, strategy)
		})
	}
}

// k8sDataPlaneCrashCase runs one strategy's crash/restore cycle.
func k8sDataPlaneCrashCase(t *testing.T, bin, strategy string) {
	t.Helper()

	p := k8sDurabilityPorts{
		aws:   freePort(t),
		k8s:   freePort(t),
		state: filepath.Join(t.TempDir(), "state.json"),
	}

	cmd := startServeK8s(t, bin, p, strategy, "150ms")

	eks := newEKSClientMain(t, "http://127.0.0.1:"+p.aws)
	endpoint := describeK8sEndpoint(t, eks, "persist-cluster", true)
	uid := uidFromEndpoint(t, endpoint)

	k8s := insecureK8sClient()

	// Apply a Deployment + Service ONLY over the k8s data plane — no provider
	// mutation — so a save proves the pure-kubectl dirty seam.
	if code, b := k8sDo(t, k8s, http.MethodPost,
		endpoint+"/apis/apps/v1/namespaces/default/deployments", persistDeploymentBody); code != http.StatusCreated {
		t.Fatalf("create Deployment = %d, want 201: %s", code, b)
	}

	if code, b := k8sDo(t, k8s, http.MethodPost,
		endpoint+"/api/v1/namespaces/default/services", persistServiceBody); code != http.StatusCreated {
		t.Fatalf("create Service = %d, want 201: %s", code, b)
	}

	// The synchronous reconciler brings the Pods up Running immediately.
	waitPodsRunning(t, k8s, endpoint, 2, 5*time.Second)

	// Wait for a background save capturing the cluster UID + the Deployment to
	// land BEFORE the hard kill — the durability window under test.
	waitFileContains(t, p.state, uid, 5*time.Second)
	waitFileContains(t, p.state, "persist-deploy", 5*time.Second)

	killHard(t, cmd)

	cmd2 := startServeK8s(t, bin, p, strategy, "150ms")
	defer killHard(t, cmd2)

	// (a) DescribeCluster returns the SAME endpoint/UID after the restart.
	endpoint2 := describeK8sEndpoint(t, eks, "persist-cluster", false)
	if endpoint2 != endpoint {
		t.Fatalf("post-restart endpoint = %q, want %q (UID round-trip broke)", endpoint2, endpoint)
	}

	// (b)+(c) The OLD kubeconfig endpoint still serves the surviving resources.
	k8s2 := insecureK8sClient()

	if code, b := k8sDo(t, k8s2, http.MethodGet,
		endpoint+"/apis/apps/v1/namespaces/default/deployments/persist-deploy", ""); code != http.StatusOK {
		t.Fatalf("GET Deployment after crash = %d, want 200 (data plane did not survive): %s", code, b)
	}

	if code, b := k8sDo(t, k8s2, http.MethodGet,
		endpoint+"/api/v1/namespaces/default/services/persist-svc", ""); code != http.StatusOK {
		t.Fatalf("GET Service after crash = %d, want 200: %s", code, b)
	}

	assertEndpointsBackPods(t, k8s2, endpoint)
	waitPodsRunning(t, k8s2, endpoint, 2, 5*time.Second)
}

// waitPodsRunning polls the Pod list until at least want Pods report Running.
func waitPodsRunning(t *testing.T, c *http.Client, endpoint string, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		code, b := k8sDo(t, c, http.MethodGet, endpoint+"/api/v1/namespaces/default/pods?labelSelector=app%3Dpersist", "")
		if code == http.StatusOK {
			if running := countRunningPods(t, b); running >= want {
				return
			}
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("did not observe %d Running pods within %s", want, timeout)
}

func countRunningPods(t *testing.T, listBody []byte) int {
	t.Helper()

	var list struct {
		Items []struct {
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(listBody, &list); err != nil {
		t.Fatalf("decode pod list: %v", err)
	}

	n := 0

	for _, it := range list.Items {
		if it.Status.Phase == "Running" {
			n++
		}
	}

	return n
}

// assertEndpointsBackPods asserts the Service's Endpoints object survived with at
// least one backing address — the reconciler-materialized wiring that must
// round-trip, not be recomputed from scratch.
func assertEndpointsBackPods(t *testing.T, c *http.Client, endpoint string) {
	t.Helper()

	code, b := k8sDo(t, c, http.MethodGet, endpoint+"/api/v1/namespaces/default/endpoints/persist-svc", "")
	if code != http.StatusOK {
		t.Fatalf("GET Endpoints after crash = %d, want 200: %s", code, b)
	}

	var ep struct {
		Subsets []struct {
			Addresses []struct {
				IP string `json:"ip"`
			} `json:"addresses"`
		} `json:"subsets"`
	}

	if err := json.Unmarshal(b, &ep); err != nil {
		t.Fatalf("decode endpoints: %v", err)
	}

	total := 0
	for _, s := range ep.Subsets {
		total += len(s.Addresses)
	}

	if total == 0 {
		t.Fatal("restored Service Endpoints has no backing addresses; endpoint wiring did not survive")
	}
}

// uidFromEndpoint extracts the <uid> segment from a …/k8s/<uid> endpoint.
func uidFromEndpoint(t *testing.T, endpoint string) string {
	t.Helper()

	i := strings.LastIndex(endpoint, "/k8s/")
	if i < 0 {
		t.Fatalf("endpoint %q has no /k8s/<uid> segment", endpoint)
	}

	return endpoint[i+len("/k8s/"):]
}

package chaos_test

import (
	"context"
	"testing"
	"time"

	regdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	logdriver "github.com/stackshy/cloudemu/logging/driver"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
)

// Baseline tests exercise every wrapped op against an engine with no scenarios
// applied, so the success path through each wrapper is covered. Errors from
// the inner drivers (e.g. NotFound on Get without a prior Create) are ignored
// — the goal here is to land coverage on the post-applyChaos delegation line,
// not to validate inner-driver behaviour, which is tested in provider tests.

func TestWrapServerlessBaseline(t *testing.T) {
	s, _ := newChaosServerless(t)
	ctx := context.Background()

	cfg := serverlessdriver.FunctionConfig{Name: "b", Runtime: "go1.x", Handler: "main"}
	_, _ = s.CreateFunction(ctx, cfg)
	_, _ = s.GetFunction(ctx, "b")
	_, _ = s.ListFunctions(ctx)
	_, _ = s.UpdateFunction(ctx, "b", cfg)
	_, _ = s.Invoke(ctx, serverlessdriver.InvokeInput{FunctionName: "b", Payload: []byte("{}")})
	_ = s.DeleteFunction(ctx, "b")
}

func TestWrapNetworkingBaseline(t *testing.T) {
	n, _ := newChaosNetworking(t)
	ctx := context.Background()

	v, _ := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	_, _ = n.DescribeVPCs(ctx, nil)
	s, _ := n.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	_, _ = n.DescribeSubnets(ctx, nil)
	sg, _ := n.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "sg", VPCID: v.ID})
	_, _ = n.DescribeSecurityGroups(ctx, nil)
	rule := netdriver.SecurityRule{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"}
	_ = n.AddIngressRule(ctx, sg.ID, rule)
	_ = n.AddEgressRule(ctx, sg.ID, rule)
	_ = n.DeleteSecurityGroup(ctx, sg.ID)
	_ = n.DeleteSubnet(ctx, s.ID)
	_ = n.DeleteVPC(ctx, v.ID)
}

func TestWrapMonitoringBaseline(t *testing.T) {
	m, _ := newChaosMonitoring(t)
	ctx := context.Background()

	_ = m.PutMetricData(ctx, []mondriver.MetricDatum{{Namespace: "x", MetricName: "y", Value: 1, Timestamp: time.Now()}})
	_, _ = m.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "x", MetricName: "y",
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Period: 60, Stat: "Average",
	})
	_, _ = m.ListMetrics(ctx, "x")
	_ = m.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "a", Namespace: "x", MetricName: "y",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 1, Period: 60, EvaluationPeriods: 1, Stat: "Average",
	})
	_, _ = m.DescribeAlarms(ctx, nil)
	_ = m.DeleteAlarm(ctx, "a")
}

func TestWrapIAMBaseline(t *testing.T) {
	i, _ := newChaosIAM(t)
	ctx := context.Background()

	_, _ = i.CreateUser(ctx, iamdriver.UserConfig{Name: "u"})
	_, _ = i.GetUser(ctx, "u")
	_, _ = i.ListUsers(ctx)
	_, _ = i.CreateRole(ctx, iamdriver.RoleConfig{Name: "r"})
	_, _ = i.GetRole(ctx, "r")
	_, _ = i.ListRoles(ctx)
	p, _ := i.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: "p", PolicyDocument: "{}"})
	if p != nil {
		_, _ = i.GetPolicy(ctx, p.ARN)
	}
	_, _ = i.ListPolicies(ctx)
	_, _ = i.CheckPermission(ctx, "u", "s3:GetObject", "*")
	if p != nil {
		_ = i.DeletePolicy(ctx, p.ARN)
	}
	_ = i.DeleteRole(ctx, "r")
	_ = i.DeleteUser(ctx, "u")
}

func TestWrapDNSBaseline(t *testing.T) {
	d, _ := newChaosDNS(t)
	ctx := context.Background()

	z, _ := d.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "b.test."})
	_, _ = d.GetZone(ctx, z.ID)
	_, _ = d.ListZones(ctx)
	rec := dnsdriver.RecordConfig{ZoneID: z.ID, Name: "x.b.test.", Type: "A", TTL: 300, Values: []string{"1.2.3.4"}}
	_, _ = d.CreateRecord(ctx, rec)
	_, _ = d.GetRecord(ctx, z.ID, "x.b.test.", "A")
	_, _ = d.ListRecords(ctx, z.ID)
	rec.TTL = 600
	_, _ = d.UpdateRecord(ctx, rec)
	_ = d.DeleteRecord(ctx, z.ID, "x.b.test.", "A")
	_ = d.DeleteZone(ctx, z.ID)
}

func TestWrapLoadBalancerBaseline(t *testing.T) {
	l, _ := newChaosLoadBalancer(t)
	ctx := context.Background()

	lb, _ := l.CreateLoadBalancer(ctx, lbdriver.LBConfig{Name: "b", Type: "application"})
	_, _ = l.DescribeLoadBalancers(ctx, nil)
	tg, _ := l.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{Name: "tg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1"})
	_, _ = l.DescribeTargetGroups(ctx, nil)
	if tg != nil {
		_ = l.RegisterTargets(ctx, tg.ARN, []lbdriver.Target{{ID: "i-1", Port: 80}})
		_, _ = l.DescribeTargetHealth(ctx, tg.ARN)
		_ = l.DeregisterTargets(ctx, tg.ARN, []lbdriver.Target{{ID: "i-1", Port: 80}})
		_ = l.DeleteTargetGroup(ctx, tg.ARN)
	}
	if lb != nil {
		_ = l.DeleteLoadBalancer(ctx, lb.ARN)
	}
}

func TestWrapMessageQueueBaseline(t *testing.T) {
	q, _ := newChaosMessageQueue(t)
	ctx := context.Background()

	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "b"})
	_, _ = q.ListQueues(ctx, "")
	if qi != nil {
		_, _ = q.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: qi.URL, Body: "x"})
		_, _ = q.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: qi.URL, MaxMessages: 1})
		_ = q.DeleteMessage(ctx, qi.URL, "rh")
		_, _ = q.SendMessageBatch(ctx, qi.URL, []mqdriver.BatchSendEntry{{ID: "1", Body: "a"}})
		_, _ = q.DeleteMessageBatch(ctx, qi.URL, []mqdriver.BatchDeleteEntry{{ID: "1", ReceiptHandle: "rh"}})
		_ = q.DeleteQueue(ctx, qi.URL)
	}
}

func TestWrapCacheBaseline(t *testing.T) {
	c, _ := newChaosCache(t)
	ctx := context.Background()

	_ = c.Set(ctx, chaosTestCacheName, chaosTestCacheKey, []byte("v"), time.Minute)
	_, _ = c.Get(ctx, chaosTestCacheName, chaosTestCacheKey)
	_, _ = c.Keys(ctx, chaosTestCacheName, "*")
	_, _ = c.Incr(ctx, chaosTestCacheName, "ctr")
	_, _ = c.IncrBy(ctx, chaosTestCacheName, "ctr", 5)
	_, _ = c.Decr(ctx, chaosTestCacheName, "ctr")
	_, _ = c.DecrBy(ctx, chaosTestCacheName, "ctr", 3)
	_ = c.Delete(ctx, chaosTestCacheName, chaosTestCacheKey)
}

func TestWrapSecretsBaseline(t *testing.T) {
	s, _ := newChaosSecrets(t)
	ctx := context.Background()

	_, _ = s.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "b"}, []byte("v"))
	_, _ = s.GetSecret(ctx, "b")
	_, _ = s.ListSecrets(ctx)
	_, _ = s.PutSecretValue(ctx, "b", []byte("v2"))
	_, _ = s.GetSecretValue(ctx, "b", "")
	_, _ = s.ListSecretVersions(ctx, "b")
	_ = s.DeleteSecret(ctx, "b")
}

func TestWrapLoggingBaseline(t *testing.T) {
	l, _ := newChaosLogging(t)
	ctx := context.Background()

	_, _ = l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "b"})
	_, _ = l.GetLogGroup(ctx, "b")
	_, _ = l.ListLogGroups(ctx)
	_, _ = l.CreateLogStream(ctx, "b", "s")
	_ = l.PutLogEvents(ctx, "b", "s", []logdriver.LogEvent{{Timestamp: time.Now(), Message: "x"}})
	_, _ = l.GetLogEvents(ctx, &logdriver.LogQueryInput{
		LogGroup: "b", StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Limit: 10,
	})
	_, _ = l.FilterLogEvents(ctx, &logdriver.FilterLogEventsInput{
		LogGroup: "b", StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Limit: 10,
	})
	_ = l.DeleteLogGroup(ctx, "b")
}

func TestWrapNotificationBaseline(t *testing.T) {
	n, _ := newChaosNotification(t)
	ctx := context.Background()

	tp, _ := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "b"})
	_, _ = n.ListTopics(ctx)
	if tp != nil {
		_, _ = n.GetTopic(ctx, tp.ID)
		sub, _ := n.Subscribe(ctx, notifdriver.SubscriptionConfig{TopicID: tp.ID, Protocol: "email", Endpoint: "x@y.z"})
		_, _ = n.Publish(ctx, notifdriver.PublishInput{TopicID: tp.ID, Message: "hi"})
		if sub != nil {
			_ = n.Unsubscribe(ctx, sub.ID)
		}
		_ = n.DeleteTopic(ctx, tp.ID)
	}
}

func TestWrapContainerRegistryBaseline(t *testing.T) {
	r, _ := newChaosContainerRegistry(t)
	ctx := context.Background()

	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "b"})
	_, _ = r.GetRepository(ctx, "b")
	_, _ = r.ListRepositories(ctx)
	m := &regdriver.ImageManifest{Repository: "b", Tag: "v1", Digest: "sha256:abc", SizeBytes: 100}
	_, _ = r.PutImage(ctx, m)
	_, _ = r.GetImage(ctx, "b", "v1")
	_, _ = r.ListImages(ctx, "b")
	_ = r.DeleteImage(ctx, "b", "v1")
	_ = r.DeleteRepository(ctx, "b", false)
}

func TestWrapEventBusBaseline(t *testing.T) {
	b, _ := newChaosEventBus(t)
	ctx := context.Background()

	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "b"})
	_, _ = b.GetEventBus(ctx, "b")
	_, _ = b.ListEventBuses(ctx)
	cfg := &ebdriver.RuleConfig{Name: "rule", EventBus: "b", EventPattern: `{"source":["x"]}`, State: "ENABLED"}
	_, _ = b.PutRule(ctx, cfg)
	_, _ = b.GetRule(ctx, "b", "rule")
	_, _ = b.ListRules(ctx, "b")
	events := []ebdriver.Event{{Source: "x", DetailType: "y", Detail: "{}", EventBus: "b", Time: time.Now()}}
	_, _ = b.PutEvents(ctx, events)
	_ = b.DeleteRule(ctx, "b", "rule")
	_ = b.DeleteEventBus(ctx, "b")
}


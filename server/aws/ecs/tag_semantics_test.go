package ecs_test

import (
	"context"
	"fmt"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const testASGArn = "arn:aws:autoscaling:us-east-1:000000000000:autoScalingGroup:x:autoScalingGroupName/asg"

// serviceTaskTags returns the tags (include=TAGS) of the service's RUNNING
// tasks that belong to its current PRIMARY deployment.
func serviceTaskTags(t *testing.T, client *awsecs.Client, ctx context.Context, service string) []map[string]string {
	t.Helper()

	svc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String("prod"), Services: []string{service},
	})
	if err != nil || len(svc.Services) != 1 {
		t.Fatalf("DescribeServices: %v", err)
	}

	var primary string

	for _, d := range svc.Services[0].Deployments {
		if aws.ToString(d.Status) == "PRIMARY" {
			primary = aws.ToString(d.Id)
		}
	}

	list, err := client.ListTasks(ctx, &awsecs.ListTasksInput{
		Cluster: aws.String("prod"), ServiceName: aws.String(service), DesiredStatus: ecstypes.DesiredStatusRunning,
	})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"), Tasks: list.TaskArns,
		Include: []ecstypes.TaskField{ecstypes.TaskFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	var out []map[string]string

	for i := range desc.Tasks {
		if aws.ToString(desc.Tasks[i].StartedBy) == primary {
			out = append(out, tagMap(desc.Tasks[i].Tags))
		}
	}

	if len(out) == 0 {
		t.Fatalf("service %s has no running tasks in deployment %s", service, primary)
	}

	return out
}

func sdkTags(kv ...string) []ecstypes.Tag {
	out := make([]ecstypes.Tag, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, ecstypes.Tag{Key: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}

	return out
}

// TestSDKServicePropagateTags guards that a service's tasks carry exactly the
// tags its propagateTags setting selects, read at launch time from the live tag
// store: NONE (the default) propagates nothing, SERVICE the service's current
// tags, TASK_DEFINITION the task definition's current tags. The service used to
// stamp its create-time tags on every task regardless of the setting, so a
// forced redeploy after an untag still launched tasks with the removed tag.
func TestSDKServicePropagateTags(t *testing.T) {
	cases := []struct {
		name      string
		propagate ecstypes.PropagateTags
		// mutate retags the propagation source between the two deployments.
		mutate      func(t *testing.T, client *awsecs.Client, ctx context.Context, svcARN, tdARN string)
		first, next map[string]string
	}{
		{
			name:  "default-none",
			first: map[string]string{}, next: map[string]string{},
		},
		{
			name: "explicit-none", propagate: ecstypes.PropagateTagsNone,
			first: map[string]string{}, next: map[string]string{},
		},
		{
			name: "service", propagate: ecstypes.PropagateTagsService,
			mutate: func(t *testing.T, client *awsecs.Client, ctx context.Context, svcARN, _ string) {
				t.Helper()
				mustUntag(t, client, ctx, svcARN, "s")
				mustTag(t, client, ctx, svcARN, sdkTags("s2", "1"))
			},
			first: map[string]string{"s": "1"}, next: map[string]string{"s2": "1"},
		},
		{
			name: "task-definition", propagate: ecstypes.PropagateTagsTaskDefinition,
			mutate: func(t *testing.T, client *awsecs.Client, ctx context.Context, _, tdARN string) {
				t.Helper()
				mustTag(t, client, ctx, tdARN, sdkTags("td2", "1"))
			},
			first: map[string]string{"td": "1"}, next: map[string]string{"td": "1", "td2": "1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, cloud := newECSServer(t)
			ctx := context.Background()

			if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
				t.Fatalf("CreateCluster: %v", err)
			}

			cloud.ECS.SeedContainerInstance("prod", "i-0prop")

			td, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
				Family: aws.String("web"),
				ContainerDefinitions: []ecstypes.ContainerDefinition{{
					Name: aws.String("app"), Image: aws.String("nginx:latest"), Memory: aws.Int32(128),
				}},
				Tags: sdkTags("td", "1"),
			})
			if err != nil {
				t.Fatalf("RegisterTaskDefinition: %v", err)
			}

			svc, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
				Cluster: aws.String("prod"), ServiceName: aws.String("s"), TaskDefinition: aws.String("web"),
				DesiredCount: aws.Int32(1), PropagateTags: tc.propagate, Tags: sdkTags("s", "1"),
			})
			if err != nil {
				t.Fatalf("CreateService: %v", err)
			}

			assertAllTaskTags(t, serviceTaskTags(t, client, ctx, "s"), tc.first, "first deployment")

			if tc.mutate != nil {
				tc.mutate(t, client, ctx, aws.ToString(svc.Service.ServiceArn),
					aws.ToString(td.TaskDefinition.TaskDefinitionArn))
			}

			if _, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
				Cluster: aws.String("prod"), Service: aws.String("s"), ForceNewDeployment: true,
			}); err != nil {
				t.Fatalf("UpdateService: %v", err)
			}

			assertAllTaskTags(t, serviceTaskTags(t, client, ctx, "s"), tc.next, "after force-new-deployment")
		})
	}
}

func assertAllTaskTags(t *testing.T, got []map[string]string, want map[string]string, stage string) {
	t.Helper()

	for _, tags := range got {
		if !maps.Equal(tags, want) {
			t.Fatalf("%s: task tags = %v, want %v", stage, tags, want)
		}
	}
}

func mustTag(t *testing.T, client *awsecs.Client, ctx context.Context, arn string, tags []ecstypes.Tag) {
	t.Helper()

	if _, err := client.TagResource(ctx, &awsecs.TagResourceInput{ResourceArn: aws.String(arn), Tags: tags}); err != nil {
		t.Fatalf("TagResource(%s): %v", arn, err)
	}
}

func mustUntag(t *testing.T, client *awsecs.Client, ctx context.Context, arn string, keys ...string) {
	t.Helper()

	if _, err := client.UntagResource(ctx, &awsecs.UntagResourceInput{ResourceArn: aws.String(arn), TagKeys: keys}); err != nil {
		t.Fatalf("UntagResource(%s): %v", arn, err)
	}
}

// TestSDKDescribeOmitsTagsWithoutInclude guards that DescribeServices,
// DescribeTaskDefinition, DescribeContainerInstances and
// DescribeCapacityProviders return tags only when include=TAGS is sent, as
// real ECS does.
func TestSDKDescribeOmitsTagsWithoutInclude(t *testing.T) {
	client, _ := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	tdARN := createTaggedTaskDef(t, client, ctx, sdkTags("k", "v"))
	registerNginx(t, client, ctx)
	createTaggedService(t, client, ctx, sdkTags("k", "v"))
	svc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String("prod"), Services: []string{"web-svc"},
	})
	if err != nil || len(svc.Services) != 1 {
		t.Fatalf("DescribeServices: %v", err)
	}

	if svc.Services[0].Tags != nil {
		t.Errorf("DescribeServices without include tags = %v, want none", svc.Services[0].Tags)
	}

	td, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{TaskDefinition: aws.String(tdARN)})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition: %v", err)
	}

	if td.Tags != nil {
		t.Errorf("DescribeTaskDefinition without include tags = %v, want none", td.Tags)
	}

	ciARN := createTaggedInstance(t, client, ctx, sdkTags("k", "v"))
	cpARN := createTaggedCapacityProvider(t, client, ctx, sdkTags("k", "v"))

	ci, err := client.DescribeContainerInstances(ctx, &awsecs.DescribeContainerInstancesInput{
		Cluster: aws.String("prod"), ContainerInstances: []string{ciARN},
	})
	if err != nil || len(ci.ContainerInstances) != 1 {
		t.Fatalf("DescribeContainerInstances: %v", err)
	}

	if ci.ContainerInstances[0].Tags != nil {
		t.Errorf("DescribeContainerInstances without include tags = %v, want none", ci.ContainerInstances[0].Tags)
	}

	cp, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{
		CapacityProviders: []string{cpARN},
	})
	if err != nil || len(cp.CapacityProviders) != 1 {
		t.Fatalf("DescribeCapacityProviders: %v", err)
	}

	if cp.CapacityProviders[0].Tags != nil {
		t.Errorf("DescribeCapacityProviders without include tags = %v, want none", cp.CapacityProviders[0].Tags)
	}
}

// TestSDKTagResourceRejectsInvalidRequests guards the TagResource validation
// real ECS applies: an unknown resource, a short-format service ARN (the
// TagResource reference requires migrating it to the long format first), a
// predefined Fargate capacity provider, more than 50 tags on a resource, and a
// key or value with the reserved aws: prefix are all InvalidParameterException.
func TestSDKTagResourceRejectsInvalidRequests(t *testing.T) {
	client, _ := newECSServer(t)
	ctx := context.Background()

	c, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	clusterARN := aws.ToString(c.Cluster.ClusterArn)

	registerNginx(t, client, ctx)
	svcARN := createTaggedService(t, client, ctx, nil)
	shortSvcARN := "arn:aws:ecs:us-east-1:000000000000:service/web-svc"

	fifty := make([]ecstypes.Tag, 0, 50)
	for i := range 50 {
		fifty = append(fifty, ecstypes.Tag{Key: aws.String(fmt.Sprintf("k%02d", i)), Value: aws.String("v")})
	}

	cases := []struct {
		name string
		arn  string
		tags []ecstypes.Tag
	}{
		{"nonexistent-cluster", "arn:aws:ecs:us-east-1:000000000000:cluster/ghost", sdkTags("a", "1")},
		{"nonexistent-service", "arn:aws:ecs:us-east-1:000000000000:service/prod/ghost", sdkTags("a", "1")},
		{"not-an-ecs-arn", "arn:aws:s3:::bucket", sdkTags("a", "1")},
		{"short-service-arn", shortSvcARN, sdkTags("a", "1")},
		{"fargate-capacity-provider", "arn:aws:ecs:us-east-1:000000000000:capacity-provider/FARGATE", sdkTags("a", "1")},
		{"fifty-one-in-one-call", clusterARN, append(append([]ecstypes.Tag{}, fifty...), sdkTags("extra", "1")...)},
		{"aws-prefixed-key", clusterARN, sdkTags("aws:owner", "x")},
		{"AWS-prefixed-key", clusterARN, sdkTags("AWS:owner", "x")},
		{"aws-prefixed-value", clusterARN, sdkTags("owner", "aws:x")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.TagResource(ctx, &awsecs.TagResourceInput{ResourceArn: aws.String(tc.arn), Tags: tc.tags})

			var ipe *ecstypes.InvalidParameterException
			if !errorsAs(err, &ipe) {
				t.Fatalf("TagResource err = %v, want InvalidParameterException", err)
			}
		})
	}

	// 50 tags fit; one more on a later call pushes the resource past the limit
	// and leaves its tags unchanged.
	mustTag(t, client, ctx, svcARN, fifty)

	_, err = client.TagResource(ctx, &awsecs.TagResourceInput{ResourceArn: aws.String(svcARN), Tags: sdkTags("k50", "v")})

	var ipe *ecstypes.InvalidParameterException
	if !errorsAs(err, &ipe) {
		t.Fatalf("TagResource(51st tag) err = %v, want InvalidParameterException", err)
	}

	list, err := client.ListTagsForResource(ctx, &awsecs.ListTagsForResourceInput{ResourceArn: aws.String(svcARN)})
	if err != nil || len(list.Tags) != 50 {
		t.Fatalf("ListTagsForResource after rejected tag = %d tags, %v; want 50", len(list.Tags), err)
	}

	// Overwriting an existing key does not count as a new tag.
	mustTag(t, client, ctx, svcARN, sdkTags("k00", "changed"))

	// Reserved keys cannot be removed, and an unknown resource cannot be untagged.
	_, err = client.UntagResource(ctx, &awsecs.UntagResourceInput{ResourceArn: aws.String(svcARN), TagKeys: []string{"aws:x"}})
	if !errorsAs(err, &ipe) {
		t.Fatalf("UntagResource(aws: key) err = %v, want InvalidParameterException", err)
	}

	_, err = client.UntagResource(ctx, &awsecs.UntagResourceInput{
		ResourceArn: aws.String("arn:aws:ecs:us-east-1:000000000000:cluster/ghost"), TagKeys: []string{"a"},
	})
	if !errorsAs(err, &ipe) {
		t.Fatalf("UntagResource(unknown) err = %v, want InvalidParameterException", err)
	}
}

// TestSDKShortServiceARNResolves guards that a short-format service ARN
// (service/<name>, no cluster segment) resolves on the service APIs that take a
// service name or ARN, while tagging it is refused (see
// TestSDKTagResourceRejectsInvalidRequests).
func TestSDKShortServiceARNResolves(t *testing.T) {
	client, _ := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	registerNginx(t, client, ctx)
	longARN := createTaggedService(t, client, ctx, nil)
	shortARN := "arn:aws:ecs:us-east-1:000000000000:service/web-svc"

	out, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String("prod"), Services: []string{shortARN},
	})
	if err != nil {
		t.Fatalf("DescribeServices: %v", err)
	}

	if len(out.Services) != 1 || aws.ToString(out.Services[0].ServiceArn) != longARN {
		t.Fatalf("DescribeServices(short ARN) = %+v, failures %+v; want %s", out.Services, out.Failures, longARN)
	}

	upd, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster: aws.String("prod"), Service: aws.String(shortARN), DesiredCount: aws.Int32(0),
	})
	if err != nil || aws.ToString(upd.Service.ServiceArn) != longARN {
		t.Fatalf("UpdateService(short ARN) = %v, %v", upd, err)
	}
}

// TestSDKCapacityProviderLifecycle drives Create/Describe/Update/Delete
// CapacityProvider through the SDK, including the predefined FARGATE and
// FARGATE_SPOT providers.
func TestSDKCapacityProviderLifecycle(t *testing.T) {
	client, _ := newECSServer(t)
	ctx := context.Background()

	all, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{})
	if err != nil {
		t.Fatalf("DescribeCapacityProviders: %v", err)
	}

	if names := capacityProviderNames(all.CapacityProviders); !maps.Equal(names, map[string]string{
		"FARGATE": "FARGATE", "FARGATE_SPOT": "FARGATE_SPOT",
	}) {
		t.Fatalf("predefined providers = %v", names)
	}

	created, err := client.CreateCapacityProvider(ctx, &awsecs.CreateCapacityProviderInput{
		Name: aws.String("asg-cp"),
		AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProvider{
			AutoScalingGroupArn: aws.String(testASGArn),
			ManagedScaling:      &ecstypes.ManagedScaling{Status: ecstypes.ManagedScalingStatusEnabled},
		},
		Tags: sdkTags("team", "a"),
	})
	if err != nil {
		t.Fatalf("CreateCapacityProvider: %v", err)
	}

	cp := created.CapacityProvider
	if cp.Status != ecstypes.CapacityProviderStatusActive || cp.Type != ecstypes.CapacityProviderTypeEc2Autoscaling ||
		aws.ToString(cp.AutoScalingGroupProvider.AutoScalingGroupArn) != testASGArn {
		t.Fatalf("created provider = %+v", cp)
	}

	ms := cp.AutoScalingGroupProvider.ManagedScaling
	if aws.ToInt32(ms.TargetCapacity) != 100 || aws.ToInt32(ms.MinimumScalingStepSize) != 1 ||
		aws.ToInt32(ms.MaximumScalingStepSize) != 10000 || aws.ToInt32(ms.InstanceWarmupPeriod) != 300 {
		t.Fatalf("managedScaling defaults = %+v", ms)
	}

	if cp.AutoScalingGroupProvider.ManagedTerminationProtection != ecstypes.ManagedTerminationProtectionDisabled {
		t.Fatalf("managedTerminationProtection = %q, want DISABLED", cp.AutoScalingGroupProvider.ManagedTerminationProtection)
	}

	expectCapacityProviderError(t, "duplicate name", func() error {
		_, err := client.CreateCapacityProvider(ctx, &awsecs.CreateCapacityProviderInput{
			Name:                     aws.String("asg-cp"),
			AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProvider{AutoScalingGroupArn: aws.String(testASGArn)},
		})

		return err
	})

	expectCapacityProviderError(t, "reserved name prefix", func() error {
		_, err := client.CreateCapacityProvider(ctx, &awsecs.CreateCapacityProviderInput{
			Name:                     aws.String("fargate-mine"),
			AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProvider{AutoScalingGroupArn: aws.String(testASGArn)},
		})

		return err
	})

	upd, err := client.UpdateCapacityProvider(ctx, &awsecs.UpdateCapacityProviderInput{
		Name: aws.String("asg-cp"),
		AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProviderUpdate{
			ManagedScaling: &ecstypes.ManagedScaling{Status: ecstypes.ManagedScalingStatusEnabled, TargetCapacity: aws.Int32(80)},
		},
	})
	if err != nil {
		t.Fatalf("UpdateCapacityProvider: %v", err)
	}

	if upd.CapacityProvider.UpdateStatus != ecstypes.CapacityProviderUpdateStatusUpdateComplete ||
		aws.ToInt32(upd.CapacityProvider.AutoScalingGroupProvider.ManagedScaling.TargetCapacity) != 80 {
		t.Fatalf("updated provider = %+v", upd.CapacityProvider)
	}

	// A provider associated with a cluster can't be deleted until it is removed.
	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.PutClusterCapacityProviders(ctx, &awsecs.PutClusterCapacityProvidersInput{
		Cluster: aws.String("prod"), CapacityProviders: []string{"asg-cp"},
		DefaultCapacityProviderStrategy: []ecstypes.CapacityProviderStrategyItem{},
	}); err != nil {
		t.Fatalf("PutClusterCapacityProviders: %v", err)
	}

	scoped, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{Cluster: aws.String("prod")})
	if err != nil || len(scoped.CapacityProviders) != 1 || aws.ToString(scoped.CapacityProviders[0].Name) != "asg-cp" {
		t.Fatalf("DescribeCapacityProviders(cluster) = %+v, %v", scoped, err)
	}

	expectCapacityProviderError(t, "delete associated", func() error {
		_, err := client.DeleteCapacityProvider(ctx, &awsecs.DeleteCapacityProviderInput{CapacityProvider: aws.String("asg-cp")})
		return err
	})

	expectCapacityProviderError(t, "delete FARGATE", func() error {
		_, err := client.DeleteCapacityProvider(ctx, &awsecs.DeleteCapacityProviderInput{CapacityProvider: aws.String("FARGATE")})
		return err
	})

	if _, err := client.PutClusterCapacityProviders(ctx, &awsecs.PutClusterCapacityProvidersInput{
		Cluster: aws.String("prod"), CapacityProviders: []string{},
		DefaultCapacityProviderStrategy: []ecstypes.CapacityProviderStrategyItem{},
	}); err != nil {
		t.Fatalf("PutClusterCapacityProviders(clear): %v", err)
	}

	del, err := client.DeleteCapacityProvider(ctx, &awsecs.DeleteCapacityProviderInput{
		CapacityProvider: cp.CapacityProviderArn,
	})
	if err != nil {
		t.Fatalf("DeleteCapacityProvider: %v", err)
	}

	if del.CapacityProvider.Status != ecstypes.CapacityProviderStatusInactive ||
		del.CapacityProvider.UpdateStatus != ecstypes.CapacityProviderUpdateStatusDeleteComplete {
		t.Fatalf("deleted provider = %+v", del.CapacityProvider)
	}

	// A deleted provider can't be tagged any more.
	_, err = client.TagResource(ctx, &awsecs.TagResourceInput{ResourceArn: cp.CapacityProviderArn, Tags: sdkTags("a", "1")})

	var ipe *ecstypes.InvalidParameterException
	if !errorsAs(err, &ipe) {
		t.Fatalf("TagResource(deleted provider) err = %v, want InvalidParameterException", err)
	}
}

func capacityProviderNames(in []ecstypes.CapacityProvider) map[string]string {
	out := make(map[string]string, len(in))
	for i := range in {
		out[aws.ToString(in[i].Name)] = string(in[i].Type)
	}

	return out
}

// expectCapacityProviderError asserts call fails with a typed ECS client error
// (InvalidParameterException or ClientException).
func expectCapacityProviderError(t *testing.T, what string, call func() error) {
	t.Helper()

	err := call()

	var (
		ipe *ecstypes.InvalidParameterException
		ce  *ecstypes.ClientException
	)

	if !errorsAs(err, &ipe) && !errorsAs(err, &ce) {
		t.Fatalf("%s: err = %v, want InvalidParameterException or ClientException", what, err)
	}
}

// TestSDKDescribeCapacityProvidersPagination guards maxResults/nextToken paging
// and the error paths of the capacity-provider wire handlers.
func TestSDKDescribeCapacityProvidersPagination(t *testing.T) {
	client, _ := newECSServer(t)
	ctx := context.Background()

	first, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{MaxResults: aws.Int32(1)})
	if err != nil {
		t.Fatalf("DescribeCapacityProviders page 1: %v", err)
	}

	if len(first.CapacityProviders) != 1 || first.NextToken == nil {
		t.Fatalf("page 1 = %d providers, nextToken %v; want 1 and a token", len(first.CapacityProviders), first.NextToken)
	}

	second, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{
		MaxResults: aws.Int32(1), NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeCapacityProviders page 2: %v", err)
	}

	if len(second.CapacityProviders) != 1 || second.NextToken != nil ||
		aws.ToString(second.CapacityProviders[0].Name) == aws.ToString(first.CapacityProviders[0].Name) {
		t.Fatalf("page 2 = %+v, nextToken %v", second.CapacityProviders, second.NextToken)
	}

	var ipe *ecstypes.InvalidParameterException

	_, err = client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{NextToken: aws.String("!!bad")})
	if !errorsAs(err, &ipe) {
		t.Fatalf("DescribeCapacityProviders(bad token) err = %v, want InvalidParameterException", err)
	}

	_, err = client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{Cluster: aws.String("ghost")})

	var cnf *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &cnf) {
		t.Fatalf("DescribeCapacityProviders(unknown cluster) err = %v, want ClusterNotFoundException", err)
	}

	expectCapacityProviderError(t, "update unknown", func() error {
		_, err := client.UpdateCapacityProvider(ctx, &awsecs.UpdateCapacityProviderInput{Name: aws.String("ghost")})
		return err
	})

	expectCapacityProviderError(t, "create without provider block", func() error {
		_, err := client.CreateCapacityProvider(ctx, &awsecs.CreateCapacityProviderInput{Name: aws.String("bare")})
		return err
	})
}

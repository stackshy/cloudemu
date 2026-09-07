package mq_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsmq "github.com/aws/aws-sdk-go-v2/service/mq"
	mqtypes "github.com/aws/aws-sdk-go-v2/service/mq/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsmq.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{MQ: cloud.MQ})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsmq.NewFromConfig(cfg, func(o *awsmq.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createBroker(t *testing.T, c *awsmq.Client, name string) *awsmq.CreateBrokerOutput {
	t.Helper()

	out, err := c.CreateBroker(context.Background(), &awsmq.CreateBrokerInput{
		BrokerName:         aws.String(name),
		EngineType:         mqtypes.EngineTypeActivemq,
		DeploymentMode:     mqtypes.DeploymentModeSingleInstance,
		HostInstanceType:   aws.String("mq.t3.micro"),
		PubliclyAccessible: aws.Bool(false),
		EngineVersion:      aws.String("5.17.6"),
		SecurityGroups:     []string{"sg-0123456789abcdef0"},
		SubnetIds:          []string{"subnet-0123456789abcdef0"},
		Logs:               &mqtypes.Logs{General: aws.Bool(true), Audit: aws.Bool(false)},
		Users: []mqtypes.User{{
			Username:      aws.String("admin"),
			Password:      aws.String("SuperSecretPass1"),
			ConsoleAccess: aws.Bool(true),
		}},
		Tags: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateBroker: %v", err)
	}

	return out
}

func describe(t *testing.T, c *awsmq.Client, id string) *awsmq.DescribeBrokerOutput {
	t.Helper()

	out, err := c.DescribeBroker(context.Background(), &awsmq.DescribeBrokerInput{BrokerId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeBroker: %v", err)
	}

	return out
}

func TestSDKBrokerLifecycle(t *testing.T) {
	c := newClient(t)

	create := createBroker(t, c, "sdk-broker")

	id := aws.ToString(create.BrokerId)
	if id == "" || aws.ToString(create.BrokerArn) == "" {
		t.Fatalf("create returned empty id/arn: %q %q", id, aws.ToString(create.BrokerArn))
	}

	d1 := describe(t, c, id)

	if d1.BrokerState != mqtypes.BrokerStateRunning {
		t.Fatalf("brokerState = %q, want RUNNING", d1.BrokerState)
	}

	if aws.ToString(d1.BrokerArn) != aws.ToString(create.BrokerArn) {
		t.Fatal("brokerArn drifted between create and describe")
	}

	assertInstances(t, d1, id)
	assertEcho(t, d1)

	// Second read: computed fields byte-identical.
	d2 := describe(t, c, id)
	if !reflect.DeepEqual(d1.BrokerInstances, d2.BrokerInstances) {
		t.Fatal("brokerInstances drifted across reads")
	}

	if !d1.Created.Equal(*d2.Created) {
		t.Fatal("created drifted across reads")
	}
}

func assertInstances(t *testing.T, d *awsmq.DescribeBrokerOutput, id string) {
	t.Helper()

	if len(d.BrokerInstances) != 1 {
		t.Fatalf("brokerInstances = %d, want 1", len(d.BrokerInstances))
	}

	inst := d.BrokerInstances[0]
	if aws.ToString(inst.ConsoleURL) != "https://"+id+".mq.us-east-1.amazonaws.com:8162" {
		t.Fatalf("consoleURL = %q", aws.ToString(inst.ConsoleURL))
	}

	if len(inst.Endpoints) != 5 {
		t.Fatalf("endpoints = %v, want 5", inst.Endpoints)
	}

	if aws.ToString(inst.IpAddress) == "" {
		t.Fatal("ActiveMQ instance missing ipAddress")
	}
}

func assertEcho(t *testing.T, d *awsmq.DescribeBrokerOutput) {
	t.Helper()

	if d.EngineType != mqtypes.EngineTypeActivemq {
		t.Fatalf("engineType = %q", d.EngineType)
	}

	if aws.ToString(d.HostInstanceType) != "mq.t3.micro" {
		t.Fatalf("hostInstanceType = %q", aws.ToString(d.HostInstanceType))
	}

	if aws.ToString(d.EngineVersion) != "5.17.6" {
		t.Fatalf("engineVersion = %q", aws.ToString(d.EngineVersion))
	}

	if len(d.Users) != 1 || aws.ToString(d.Users[0].Username) != "admin" {
		t.Fatalf("users = %+v", d.Users)
	}
}

func TestSDKUpdateBrokerLogs(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	id := aws.ToString(createBroker(t, c, "upd-broker").BrokerId)
	created := describe(t, c, id)

	if _, err := c.UpdateBroker(ctx, &awsmq.UpdateBrokerInput{
		BrokerId: aws.String(id),
		Logs:     &mqtypes.Logs{General: aws.Bool(true), Audit: aws.Bool(true)},
	}); err != nil {
		t.Fatalf("UpdateBroker: %v", err)
	}

	after := describe(t, c, id)

	if !aws.ToBool(after.Logs.Audit) {
		t.Fatal("audit logging not enabled after update")
	}

	if aws.ToString(after.BrokerArn) != aws.ToString(created.BrokerArn) || !after.Created.Equal(*created.Created) {
		t.Fatal("computed fields drifted after update")
	}
}

func TestSDKListAndDeleteBroker(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	id := aws.ToString(createBroker(t, c, "list-broker").BrokerId)

	list, err := c.ListBrokers(ctx, &awsmq.ListBrokersInput{})
	if err != nil {
		t.Fatalf("ListBrokers: %v", err)
	}

	if len(list.BrokerSummaries) != 1 || aws.ToString(list.BrokerSummaries[0].BrokerId) != id {
		t.Fatalf("BrokerSummaries = %+v", list.BrokerSummaries)
	}

	if _, err := c.DeleteBroker(ctx, &awsmq.DeleteBrokerInput{BrokerId: aws.String(id)}); err != nil {
		t.Fatalf("DeleteBroker: %v", err)
	}

	_, err = c.DescribeBroker(ctx, &awsmq.DescribeBrokerInput{BrokerId: aws.String(id)})

	var nf *mqtypes.NotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeBroker after delete: got %v, want NotFoundException", err)
	}
}

func TestSDKDuplicateBrokerConflict(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	createBroker(t, c, "dup-broker")

	_, err := c.CreateBroker(ctx, &awsmq.CreateBrokerInput{
		BrokerName:         aws.String("dup-broker"),
		EngineType:         mqtypes.EngineTypeActivemq,
		DeploymentMode:     mqtypes.DeploymentModeSingleInstance,
		HostInstanceType:   aws.String("mq.t3.micro"),
		PubliclyAccessible: aws.Bool(false),
	})

	var cf *mqtypes.ConflictException
	if !errors.As(err, &cf) {
		t.Fatalf("duplicate broker: got %v, want ConflictException", err)
	}
}

func TestSDKConfigurationRevisions(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateConfiguration(ctx, &awsmq.CreateConfigurationInput{
		Name:          aws.String("sdk-cfg"),
		EngineType:    mqtypes.EngineTypeActivemq,
		EngineVersion: aws.String("5.17.6"),
	})
	if err != nil {
		t.Fatalf("CreateConfiguration: %v", err)
	}

	id := aws.ToString(create.Id)
	if aws.ToInt32(create.LatestRevision.Revision) != 1 {
		t.Fatalf("create latest revision = %d, want 1", aws.ToInt32(create.LatestRevision.Revision))
	}

	data := "PGJyb2tlcj48L2Jyb2tlcj4=" // base64("<broker></broker>")

	up, err := c.UpdateConfiguration(ctx, &awsmq.UpdateConfigurationInput{
		ConfigurationId: aws.String(id),
		Data:            aws.String(data),
		Description:     aws.String("rev2"),
	})
	if err != nil {
		t.Fatalf("UpdateConfiguration: %v", err)
	}

	if aws.ToInt32(up.LatestRevision.Revision) != 2 {
		t.Fatalf("update latest revision = %d, want 2", aws.ToInt32(up.LatestRevision.Revision))
	}

	desc, err := c.DescribeConfiguration(ctx, &awsmq.DescribeConfigurationInput{ConfigurationId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeConfiguration: %v", err)
	}

	if aws.ToString(desc.Arn) != aws.ToString(create.Arn) || aws.ToInt32(desc.LatestRevision.Revision) != 2 {
		t.Fatal("configuration identity/revision drifted")
	}

	rev, err := c.DescribeConfigurationRevision(ctx, &awsmq.DescribeConfigurationRevisionInput{
		ConfigurationId: aws.String(id), ConfigurationRevision: aws.String("2"),
	})
	if err != nil {
		t.Fatalf("DescribeConfigurationRevision: %v", err)
	}

	if aws.ToString(rev.Data) != data {
		t.Fatalf("revision data = %q, want verbatim round-trip", aws.ToString(rev.Data))
	}
}

func TestSDKUserLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	id := aws.ToString(createBroker(t, c, "user-broker").BrokerId)

	if _, err := c.CreateUser(ctx, &awsmq.CreateUserInput{
		BrokerId: aws.String(id),
		Username: aws.String("app"),
		Password: aws.String("AppSecretPass12"),
		Groups:   []string{"dev"},
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	du, err := c.DescribeUser(ctx, &awsmq.DescribeUserInput{
		BrokerId: aws.String(id), Username: aws.String("app"),
	})
	if err != nil {
		t.Fatalf("DescribeUser: %v", err)
	}

	if len(du.Groups) != 1 || du.Groups[0] != "dev" {
		t.Fatalf("groups = %v", du.Groups)
	}

	lu, err := c.ListUsers(ctx, &awsmq.ListUsersInput{BrokerId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	if len(lu.Users) != 2 {
		t.Fatalf("users = %d, want 2 (admin + app)", len(lu.Users))
	}

	if _, err := c.DeleteUser(ctx, &awsmq.DeleteUserInput{
		BrokerId: aws.String(id), Username: aws.String("app"),
	}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err = c.DescribeUser(ctx, &awsmq.DescribeUserInput{BrokerId: aws.String(id), Username: aws.String("app")})

	var nf *mqtypes.NotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeUser after delete: got %v, want NotFoundException", err)
	}
}

func TestSDKBrokerTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	arn := aws.ToString(createBroker(t, c, "tag-broker").BrokerArn)

	if _, err := c.CreateTags(ctx, &awsmq.CreateTagsInput{
		ResourceArn: aws.String(arn), Tags: map[string]string{"team": "data"},
	}); err != nil {
		t.Fatalf("CreateTags: %v", err)
	}

	lt, err := c.ListTags(ctx, &awsmq.ListTagsInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if lt.Tags["team"] != "data" || lt.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want team=data and env=test", lt.Tags)
	}

	if _, err := c.DeleteTags(ctx, &awsmq.DeleteTagsInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"team"},
	}); err != nil {
		t.Fatalf("DeleteTags: %v", err)
	}

	lt2, _ := c.ListTags(ctx, &awsmq.ListTagsInput{ResourceArn: aws.String(arn)})
	if _, ok := lt2.Tags["team"]; ok {
		t.Fatal("team tag not removed")
	}
}

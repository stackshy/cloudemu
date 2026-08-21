package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestLambdaCompat drives the AWS Lambda control-plane lifecycle through the
// real aws-sdk-go-v2 client against CloudEmu's in-process wire server.
// Operation names match the portable "serverless" driver in
// docs/coverage/coverage.json (providers.aws = "Lambda"). Layer and
// concurrency ops are omitted (the wire handler does not route them), as are
// the event-source-mapping ops (their responses encode LastModified as an
// RFC3339 string, which the SDK cannot deserialize) — all gaps, not red cells.
func TestLambdaCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{Lambda: provider.Lambda})

	client := awslambda.NewFromConfig(sess.Config(), func(o *awslambda.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc     = "serverless"
		fnName  = "compat-fn"
		alias   = "live"
		version = "1"
	)

	sess.Op(svc, "CreateFunction", func() error {
		_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
			FunctionName: aws.String(fnName),
			Runtime:      lambdatypes.RuntimeGo1x,
			Role:         aws.String("arn:aws:iam::000000000000:role/test"),
			Handler:      aws.String("main"),
			MemorySize:   aws.Int32(128),
			Timeout:      aws.Int32(30),
			Code:         &lambdatypes.FunctionCode{ZipFile: []byte("fake-zip")},
		})

		return err
	})

	sess.Op(svc, "GetFunction", func() error {
		out, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(fnName)})
		if err != nil {
			return err
		}

		if out.Configuration == nil || aws.ToString(out.Configuration.FunctionName) != fnName {
			return fmt.Errorf("GetFunction returned %+v, want FunctionName=%s", out.Configuration, fnName)
		}

		return nil
	})

	sess.Op(svc, "ListFunctions", func() error {
		out, err := client.ListFunctions(ctx, &awslambda.ListFunctionsInput{})
		if err != nil {
			return err
		}

		for _, fn := range out.Functions {
			if aws.ToString(fn.FunctionName) == fnName {
				return nil
			}
		}

		return fmt.Errorf("function %q not found in ListFunctions", fnName)
	})

	sess.Op(svc, "UpdateFunction", func() error {
		_, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(fnName),
			MemorySize:   aws.Int32(256),
		})

		return err
	})

	sess.Op(svc, "Invoke", func() error {
		_, err := client.Invoke(ctx, &awslambda.InvokeInput{
			FunctionName: aws.String(fnName),
			Payload:      []byte(`{"ping":true}`),
		})

		return err
	})

	sess.Op(svc, "PublishVersion", func() error {
		_, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String(fnName)})
		return err
	})

	sess.Op(svc, "ListVersions", func() error {
		_, err := client.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
			FunctionName: aws.String(fnName),
		})

		return err
	})

	sess.Op(svc, "CreateAlias", func() error {
		_, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
			FunctionName:    aws.String(fnName),
			Name:            aws.String(alias),
			FunctionVersion: aws.String(version),
		})

		return err
	})

	sess.Op(svc, "GetAlias", func() error {
		_, err := client.GetAlias(ctx, &awslambda.GetAliasInput{
			FunctionName: aws.String(fnName),
			Name:         aws.String(alias),
		})

		return err
	})

	sess.Op(svc, "ListAliases", func() error {
		_, err := client.ListAliases(ctx, &awslambda.ListAliasesInput{FunctionName: aws.String(fnName)})
		return err
	})

	sess.Op(svc, "UpdateAlias", func() error {
		_, err := client.UpdateAlias(ctx, &awslambda.UpdateAliasInput{
			FunctionName:    aws.String(fnName),
			Name:            aws.String(alias),
			FunctionVersion: aws.String(version),
			Description:     aws.String("promoted"),
		})

		return err
	})

	sess.Op(svc, "DeleteAlias", func() error {
		_, err := client.DeleteAlias(ctx, &awslambda.DeleteAliasInput{
			FunctionName: aws.String(fnName),
			Name:         aws.String(alias),
		})

		return err
	})

	sess.Op(svc, "DeleteFunction", func() error {
		_, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{FunctionName: aws.String(fnName)})
		return err
	})
}

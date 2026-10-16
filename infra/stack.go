// Package main holds the DocFlow CDK application.
//
// It is its own Go module so that the CDK dependency tree, which is large and
// build-time only, stays out of the Lambda module's graph.
package main

import (
	"path/filepath"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// Stage is a deployment environment.
type Stage string

// The environments this app knows about.
const (
	StageDev  Stage = "dev"
	StageProd Stage = "prod"
)

// Valid reports whether s is one of the known stages.
func (s Stage) Valid() bool { return s == StageDev || s == StageProd }

// Retention matches the TTL internal/store writes onto every item, so a job's
// record and its bytes expire together.
const retentionDays = 7

// DocFlowStackProps configures the stack.
type DocFlowStackProps struct {
	awscdk.StackProps

	Stage Stage

	// UploadOrigins are the origins allowed to POST an upload. Browsers need
	// this for the presigned POST; it grants nothing on its own, since the
	// signed policy is what authorizes the write.
	UploadOrigins []string

	// AssetRoot holds one directory per command, each with a compiled
	// bootstrap. It defaults to ../bin, which is what `make build` fills.
	// Tests point it at a throwaway directory so they do not need the Go
	// build to have run.
	AssetRoot string
}

// defaultAssetRoot is where `make build` puts the compiled Lambdas.
const defaultAssetRoot = "../bin"

// DocFlowStack holds the storage and compute the API needs.
//
// There is deliberately no API Gateway yet: the handlers read their caller
// from a JWT authorizer, and which issuer signs those tokens is still open.
// Adding the HTTP API is the first job of the next phase.
//
// Nothing here runs when idle. DynamoDB is on-demand, Lambda bills per
// invocation, and the log groups expire, which is the one cost that otherwise
// accrues quietly.
func NewDocFlowStack(scope constructs.Construct, id string, props *DocFlowStackProps) awscdk.Stack {
	stackProps := props.StackProps
	stack := awscdk.NewStack(scope, jsii.String(id), &stackProps)

	// Dev is meant to be torn down and rebuilt; prod keeps its data even if
	// the stack goes away.
	removalPolicy := awscdk.RemovalPolicy_DESTROY
	if props.Stage == StageProd {
		removalPolicy = awscdk.RemovalPolicy_RETAIN
	}

	table := awsdynamodb.NewTable(stack, jsii.String("Table"), &awsdynamodb.TableProps{
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("pk"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		SortKey: &awsdynamodb.Attribute{
			Name: jsii.String("sk"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		// On-demand costs nothing while idle, which provisioned capacity does
		// not. This pipeline's traffic is bursty and tiny.
		BillingMode: awsdynamodb.BillingMode_PAY_PER_REQUEST,
		// internal/store writes this attribute as Unix seconds.
		TimeToLiveAttribute: jsii.String("expiresAt"),
		RemovalPolicy:       removalPolicy,
	})

	uploads := awss3.NewBucket(stack, jsii.String("Uploads"), &awss3.BucketProps{
		BlockPublicAccess: awss3.BlockPublicAccess_BLOCK_ALL(),
		Encryption:        awss3.BucketEncryption_S3_MANAGED,
		EnforceSSL:        jsii.Bool(true),
		LifecycleRules: &[]*awss3.LifecycleRule{
			{
				Id:                                  jsii.String("expire-uploads"),
				Enabled:                             jsii.Bool(true),
				Expiration:                          awscdk.Duration_Days(jsii.Number(retentionDays)),
				AbortIncompleteMultipartUploadAfter: awscdk.Duration_Days(jsii.Number(1)),
			},
		},
		Cors: &[]*awss3.CorsRule{
			{
				AllowedMethods: &[]awss3.HttpMethods{awss3.HttpMethods_POST},
				AllowedOrigins: jsii.Strings(uploadOrigins(props.UploadOrigins)...),
				AllowedHeaders: jsii.Strings("*"),
				MaxAge:         jsii.Number(3000),
			},
		},
		RemovalPolicy:     removalPolicy,
		AutoDeleteObjects: jsii.Bool(props.Stage != StageProd),
	})

	assetRoot := props.AssetRoot
	if assetRoot == "" {
		assetRoot = defaultAssetRoot
	}

	createJob := goFunction(stack, "CreateJob", "create-job", props.Stage, assetRoot, map[string]*string{
		"TABLE_NAME":    table.TableName(),
		"UPLOAD_BUCKET": uploads.BucketName(),
	})
	getJob := goFunction(stack, "GetJob", "get-job", props.Stage, assetRoot, map[string]*string{
		"TABLE_NAME": table.TableName(),
	})

	// Permissions are written out rather than taken from GrantWriteData and
	// friends, which hand over more verbs than either function uses.
	createJob.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:PutItem"),
		Resources: &[]*string{table.TableArn()},
	}))
	createJob.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		// The function signs the upload with its own role, so it needs the
		// permission it hands to the client, and no more: one verb, under one
		// prefix.
		Actions:   jsii.Strings("s3:PutObject"),
		Resources: &[]*string{uploads.ArnForObjects(jsii.String("uploads/*"))},
	}))
	getJob.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:GetItem", "dynamodb:Query"),
		Resources: &[]*string{table.TableArn()},
	}))

	awscdk.NewCfnOutput(stack, jsii.String("TableName"), &awscdk.CfnOutputProps{
		Value: table.TableName(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UploadBucket"), &awscdk.CfnOutputProps{
		Value: uploads.BucketName(),
	})

	return stack
}

// uploadOrigins defaults to every origin. The presigned policy is what
// authorizes a write, so this only decides which pages a browser will let
// try; the dashboard's origin should replace it once that exists.
func uploadOrigins(configured []string) []string {
	if len(configured) == 0 {
		return []string{"*"}
	}
	return configured
}

// goFunction wires one of the compiled Go binaries up as a Lambda.
//
// The asset is <assetRoot>/<command>, which `make build` fills with a
// linux/arm64 bootstrap. Run it before synth or the asset is missing.
func goFunction(scope constructs.Construct, id, command string, stage Stage, assetRoot string, environment map[string]*string) awslambda.Function {
	logGroup := awslogs.NewLogGroup(scope, jsii.String(id+"Logs"), &awslogs.LogGroupProps{
		// Without this the group never expires, and the bill grows on its own.
		Retention:     awslogs.RetentionDays_ONE_WEEK,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	logLevel := "debug"
	if stage == StageProd {
		logLevel = "info"
	}
	env := map[string]*string{"LOG_LEVEL": jsii.String(logLevel)}
	for key, value := range environment {
		env[key] = value
	}

	return awslambda.NewFunction(scope, jsii.String(id), &awslambda.FunctionProps{
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		Code:         awslambda.Code_FromAsset(jsii.String(filepath.Join(assetRoot, command)), nil),
		MemorySize:   jsii.Number(256),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(10)),
		Environment:  &env,
		LogGroup:     logGroup,
	})
}

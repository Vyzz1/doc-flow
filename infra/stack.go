// Package main holds the DocFlow CDK application.
//
// It is its own Go module so that the CDK dependency tree, which is large and
// build-time only, stays out of the Lambda module's graph.
package main

import (
	"path/filepath"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecrassets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdaeventsources"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3notifications"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
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

// DocFlowStack holds everything the service runs on: the table, the upload
// bucket, the functions, the queue, the workflow, the user directory, and
// the HTTP API in front of it all.
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

	// The workflow's steps. Only validate reads the uploaded object; finish
	// only writes a status.
	validate := goFunction(stack, "Validate", "validate", props.Stage, assetRoot, map[string]*string{
		"TABLE_NAME":    table.TableName(),
		"UPLOAD_BUCKET": uploads.BucketName(),
	})
	// Recognize ships as a container image: it is the only step that needs
	// the Tesseract engine and poppler, neither of which Lambda's managed
	// runtimes carry. It also gets the most memory and the longest timeout,
	// because OCR dominates the pipeline's runtime and Lambda scales CPU
	// with memory.
	recognize := awslambda.NewDockerImageFunction(stack, jsii.String("Recognize"), &awslambda.DockerImageFunctionProps{
		Code: awslambda.DockerImageCode_FromImageAsset(jsii.String("../build/ocr"), &awslambda.AssetImageCodeProps{
			Platform: awsecrassets.Platform_LINUX_ARM64(),
		}),
		Architecture: awslambda.Architecture_ARM_64(),
		MemorySize:   jsii.Number(2048),
		Timeout:      awscdk.Duration_Minutes(jsii.Number(2)),
		Environment: &map[string]*string{
			"LOG_LEVEL":     jsii.String(logLevelFor(props.Stage)),
			"TABLE_NAME":    table.TableName(),
			"UPLOAD_BUCKET": uploads.BucketName(),
			"OCR_PROVIDER":  jsii.String("tesseract"),
		},
		LogGroup: newLogGroup(stack, "Recognize"),
	})

	extract := goFunction(stack, "Extract", "extract", props.Stage, assetRoot, map[string]*string{
		"TABLE_NAME":    table.TableName(),
		"UPLOAD_BUCKET": uploads.BucketName(),
	})

	finish := goFunction(stack, "Finish", "finish", props.Stage, assetRoot, map[string]*string{
		"TABLE_NAME": table.TableName(),
	})

	workflow := newWorkflow(stack, validate, recognize, extract, finish, removalPolicy)

	users := newIdentity(stack, props.Stage, removalPolicy)
	api := newHttpApi(stack, users, createJob, getJob)

	// A dead-letter queue the main queue gives up into, rather than
	// redelivering a message that has already failed three times.
	deadLetters := awssqs.NewQueue(stack, jsii.String("UploadsDLQ"), &awssqs.QueueProps{
		RetentionPeriod: awscdk.Duration_Days(jsii.Number(retentionDays)),
		EnforceSSL:      jsii.Bool(true),
		RemovalPolicy:   removalPolicy,
	})
	queue := awssqs.NewQueue(stack, jsii.String("UploadsQueue"), &awssqs.QueueProps{
		// Longer than the starter's timeout, or SQS would hand the same
		// message to a second invocation while the first is still working.
		VisibilityTimeout: awscdk.Duration_Seconds(jsii.Number(180)),
		RetentionPeriod:   awscdk.Duration_Days(jsii.Number(retentionDays)),
		EnforceSSL:        jsii.Bool(true),
		DeadLetterQueue: &awssqs.DeadLetterQueue{
			Queue:           deadLetters,
			MaxReceiveCount: jsii.Number(3),
		},
		RemovalPolicy: removalPolicy,
	})

	// Only uploads start a workflow. The derived artifacts this pipeline
	// writes land under other prefixes, and reacting to those would loop.
	uploads.AddEventNotification(
		awss3.EventType_OBJECT_CREATED,
		awss3notifications.NewSqsDestination(queue),
		&awss3.NotificationKeyFilter{Prefix: jsii.String("uploads/")},
	)

	starter := goFunction(stack, "Starter", "starter", props.Stage, assetRoot, map[string]*string{
		"TABLE_NAME":        table.TableName(),
		"STATE_MACHINE_ARN": workflow.StateMachineArn(),
	})
	starter.AddEventSource(awslambdaeventsources.NewSqsEventSource(queue, &awslambdaeventsources.SqsEventSourceProps{
		BatchSize: jsii.Number(10),
		// The starter answers with the messages that failed, so SQS
		// redelivers only those rather than the whole batch.
		ReportBatchItemFailures: jsii.Bool(true),
	}))

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

	// The starter reads a job to check it exists, moves it to PROCESSING,
	// and launches one execution. It never reads the document.
	starter.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:GetItem", "dynamodb:UpdateItem"),
		Resources: &[]*string{table.TableArn()},
	}))
	starter.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("states:StartExecution"),
		Resources: &[]*string{workflow.StateMachineArn()},
	}))

	// Validate reads the object's head and records one step result. It
	// cannot write a job's status, which is the finish step's job alone.
	validate.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:GetItem", "dynamodb:PutItem"),
		Resources: &[]*string{table.TableArn()},
	}))
	validate.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("s3:GetObject"),
		Resources: &[]*string{uploads.ArnForObjects(jsii.String("uploads/*"))},
	}))

	// Recognize reads the upload, writes the three renditions, and records
	// one step result. It cannot change a job's status.
	recognize.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:PutItem"),
		Resources: &[]*string{table.TableArn()},
	}))
	recognize.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("s3:GetObject"),
		Resources: &[]*string{uploads.ArnForObjects(jsii.String("uploads/*"))},
	}))
	recognize.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		// Written under their own prefix, which is also the prefix the
		// upload notification ignores.
		Actions:   jsii.Strings("s3:PutObject"),
		Resources: &[]*string{uploads.ArnForObjects(jsii.String("derived/*"))},
	}))

	// Extract reads the recognized text back and stores the result. It
	// never touches the customer's original document.
	extract.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:PutItem"),
		Resources: &[]*string{table.TableArn()},
	}))
	extract.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("s3:GetObject"),
		Resources: &[]*string{uploads.ArnForObjects(jsii.String("derived/*"))},
	}))

	finish.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("dynamodb:UpdateItem"),
		Resources: &[]*string{table.TableArn()},
	}))

	awscdk.NewCfnOutput(stack, jsii.String("TableName"), &awscdk.CfnOutputProps{
		Value: table.TableName(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UploadBucket"), &awscdk.CfnOutputProps{
		Value: uploads.BucketName(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("ApiUrl"), &awscdk.CfnOutputProps{
		Value: api.Url(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UserPoolId"), &awscdk.CfnOutputProps{
		Value: users.pool.UserPoolId(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UserPoolClientId"), &awscdk.CfnOutputProps{
		Value: users.client.UserPoolClientId(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("StateMachine"), &awscdk.CfnOutputProps{
		Value: workflow.StateMachineArn(),
	})
	awscdk.NewCfnOutput(stack, jsii.String("DeadLetterQueue"), &awscdk.CfnOutputProps{
		Value: deadLetters.QueueUrl(),
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
// newLogGroup gives a function a group that expires. Without one the group
// never expires, and the bill grows on its own.
func newLogGroup(scope constructs.Construct, id string) awslogs.LogGroup {
	return awslogs.NewLogGroup(scope, jsii.String(id+"Logs"), &awslogs.LogGroupProps{
		Retention:     awslogs.RetentionDays_ONE_WEEK,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})
}

// logLevelFor keeps dev noisy and production quiet.
func logLevelFor(stage Stage) string {
	if stage == StageProd {
		return "info"
	}
	return "debug"
}

func goFunction(scope constructs.Construct, id, command string, stage Stage, assetRoot string, environment map[string]*string) awslambda.Function {
	logGroup := newLogGroup(scope, id)

	env := map[string]*string{"LOG_LEVEL": jsii.String(logLevelFor(stage))}
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

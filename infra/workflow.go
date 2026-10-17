package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctionstasks"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// Error names the steps return, matched by the retry and catch rules below.
// internal/pipeline has a test pinning them to its Go type names, because
// the Lambda runtime reports a returned error's bare type name and renaming
// one would silently stop these rules matching.
const (
	transientErrorName = "TransientError"
	permanentErrorName = "PermanentError"
)

// newWorkflow builds the processing state machine.
//
// It is deliberately short. Step Functions Standard bills per state
// transition and the free tier allows four thousand a month, so every state
// has to earn its place: validate, recognize, extract, then finish, with one
// catch per working state routing a failure to the same finish function.
// docs/adr/0003-recognition-is-one-state.md explains why rendering, the
// thumbnail and the OCR are one state rather than three.
func newWorkflow(scope constructs.Construct, validate, recognize, extract, finish awslambda.Function, removalPolicy awscdk.RemovalPolicy) awsstepfunctions.StateMachine {
	markDone := awsstepfunctionstasks.NewLambdaInvoke(scope, jsii.String("MarkDoneStep"), &awsstepfunctionstasks.LambdaInvokeProps{
		StateName:      jsii.String("MarkDone"),
		LambdaFunction: finish,
		// Hand the step the payload itself rather than Lambda's envelope,
		// and keep its answer out of the result: nothing reads it.
		Payload:                  awsstepfunctions.TaskInput_FromJsonPathAt(jsii.String("$")),
		OutputPath:               jsii.String("$.Payload"),
		RetryOnServiceExceptions: jsii.Bool(true),
	})
	retryTransient(markDone)

	// The failure path reuses the same function. A second Lambda would cost
	// another cold start and another transition for one boolean's
	// difference.
	markFailed := awsstepfunctionstasks.NewLambdaInvoke(scope, jsii.String("MarkFailedStep"), &awsstepfunctionstasks.LambdaInvokeProps{
		StateName:      jsii.String("MarkFailed"),
		LambdaFunction: finish,
		Payload: awsstepfunctions.TaskInput_FromObject(&map[string]any{
			"jobId.$":     "$.jobId",
			"userId.$":    "$.userId",
			"docType.$":   "$.docType",
			"uploadKey.$": "$.uploadKey",
			"failed":      true,
			// The catch block put the error here. Turning it into a reason
			// a user may read happens in Go, where it is tested.
			"error.$": "$.error",
		}),
		OutputPath:               jsii.String("$.Payload"),
		RetryOnServiceExceptions: jsii.Bool(true),
	})
	retryTransient(markFailed)

	// The construct id differs from the state name because a function in
	// this stack is already called Validate; the state is what shows in the
	// console, so it keeps the shorter name.
	validateTask := awsstepfunctionstasks.NewLambdaInvoke(scope, jsii.String("ValidateStep"), &awsstepfunctionstasks.LambdaInvokeProps{
		StateName:                jsii.String("Validate"),
		LambdaFunction:           validate,
		Payload:                  awsstepfunctions.TaskInput_FromJsonPathAt(jsii.String("$")),
		OutputPath:               jsii.String("$.Payload"),
		RetryOnServiceExceptions: jsii.Bool(true),
	})
	retryTransient(validateTask)
	recognizeTask := awsstepfunctionstasks.NewLambdaInvoke(scope, jsii.String("RecognizeStep"), &awsstepfunctionstasks.LambdaInvokeProps{
		StateName:                jsii.String("Recognize"),
		LambdaFunction:           recognize,
		Payload:                  awsstepfunctions.TaskInput_FromJsonPathAt(jsii.String("$")),
		OutputPath:               jsii.String("$.Payload"),
		RetryOnServiceExceptions: jsii.Bool(true),
	})
	retryTransient(recognizeTask)

	extractTask := awsstepfunctionstasks.NewLambdaInvoke(scope, jsii.String("ExtractStep"), &awsstepfunctionstasks.LambdaInvokeProps{
		StateName:                jsii.String("Extract"),
		LambdaFunction:           extract,
		Payload:                  awsstepfunctions.TaskInput_FromJsonPathAt(jsii.String("$")),
		OutputPath:               jsii.String("$.Payload"),
		RetryOnServiceExceptions: jsii.Bool(true),
	})
	retryTransient(extractTask)

	// Every state that can fail routes a failure to the same place,
	// MarkDone included. Without a catch of its own, a state's failure ends
	// the execution with the job still PROCESSING, and nothing would ever
	// move it: it would sit there until its TTL removed it.
	//
	// MarkFailed has no catch. If marking a job failed itself fails, there
	// is nothing further the workflow can do, and the failed execution is
	// what says so.
	for _, task := range []awsstepfunctionstasks.LambdaInvoke{validateTask, recognizeTask, extractTask, markDone} {
		task.AddCatch(markFailed, &awsstepfunctions.CatchProps{
			// Everything, including a permanent failure, which is caught
			// rather than retried: the retry rule above names only the
			// transient one.
			Errors: jsii.Strings("States.ALL"),
			// Keep the input, and put the error beside it, so MarkFailed
			// still knows which job it is finishing.
			ResultPath: jsii.String("$.error"),
		})
	}

	logs := awslogs.NewLogGroup(scope, jsii.String("WorkflowLogs"), &awslogs.LogGroupProps{
		Retention:     awslogs.RetentionDays_ONE_WEEK,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	return awsstepfunctions.NewStateMachine(scope, jsii.String("Workflow"), &awsstepfunctions.StateMachineProps{
		// Standard, not Express: executions are long and rare, and Express
		// bills for duration whether or not anything is happening.
		StateMachineType: awsstepfunctions.StateMachineType_STANDARD,
		DefinitionBody: awsstepfunctions.DefinitionBody_FromChainable(
			validateTask.Next(recognizeTask).Next(extractTask).Next(markDone),
		),
		Timeout: awscdk.Duration_Minutes(jsii.Number(15)),
		Logs: &awsstepfunctions.LogOptions{
			Destination: logs,
			Level:       awsstepfunctions.LogLevel_ERROR,
		},
		RemovalPolicy: removalPolicy,
	})
}

// retryTransient backs off on the failures worth repeating, and only those.
// A permanent failure falls through to the catch immediately, which is what
// keeps a malformed upload from occupying the workflow for a minute.
func retryTransient(task awsstepfunctionstasks.LambdaInvoke) {
	task.AddRetry(&awsstepfunctions.RetryProps{
		Errors:      jsii.Strings(transientErrorName),
		Interval:    awscdk.Duration_Seconds(jsii.Number(2)),
		MaxAttempts: jsii.Number(3),
		BackoffRate: jsii.Number(2),
		// Jitter, so a throttled dependency does not get every retry at the
		// same instant.
		JitterStrategy: awsstepfunctions.JitterType_FULL,
	})
}

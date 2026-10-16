package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

// assetRoot holds throwaway stand-ins for the compiled Lambdas, so these
// tests do not need `make build` to have run. The stack only checks that each
// asset directory exists; what the file contains never reaches an assertion.
var assetRoot string

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "docflow-assets")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create asset root:", err)
		os.Exit(1)
	}
	for _, command := range []string{"create-job", "get-job", "starter", "validate", "finish"} {
		dir := filepath.Join(root, command)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "create asset directory:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dir, "bootstrap"), []byte("stand-in"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write bootstrap:", err)
			os.Exit(1)
		}
	}
	assetRoot = root

	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, "remove asset root:", err)
	}
	os.Exit(code)
}

// Synthesizing goes through jsii, which drives the CDK's JavaScript in a Node
// subprocess, so it costs seconds rather than milliseconds. Each stage is
// built once and shared; the assertions only read the result.
var (
	devTemplate  = sync.OnceValue(func() assertions.Template { return synthesize(StageDev) })
	prodTemplate = sync.OnceValue(func() assertions.Template { return synthesize(StageProd) })
)

// template returns the synthesized template for one stage.
func template(t *testing.T, stage Stage) assertions.Template {
	t.Helper()
	if stage == StageProd {
		return prodTemplate()
	}
	return devTemplate()
}

func synthesize(stage Stage) assertions.Template {
	app := awscdk.NewApp(nil)
	stack := NewDocFlowStack(app, "DocFlow-"+string(stage), &DocFlowStackProps{
		StackProps: awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String("123456789012"),
				Region:  jsii.String("ap-southeast-1"),
			},
		},
		Stage:     stage,
		AssetRoot: assetRoot,
	})
	return assertions.Template_FromStack(stack, nil)
}

// resources returns every resource of a type, decoded into plain Go values so
// the assertions read like ordinary tests.
func resources(t *testing.T, tpl assertions.Template, resourceType string) map[string]map[string]any {
	t.Helper()
	raw := tpl.FindResources(jsii.String(resourceType), nil)
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal %s: %v", resourceType, err)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", resourceType, err)
	}
	return out
}

// only returns the single resource of a type, failing if there is not exactly
// one.
func only(t *testing.T, tpl assertions.Template, resourceType string) map[string]any {
	t.Helper()
	found := resources(t, tpl, resourceType)
	if len(found) != 1 {
		t.Fatalf("found %d resources of type %s, want 1", len(found), resourceType)
	}
	for _, resource := range found {
		return resource
	}
	return nil
}

// properties digs the Properties object out of a resource.
func properties(t *testing.T, resource map[string]any) map[string]any {
	t.Helper()
	props, ok := resource["Properties"].(map[string]any)
	if !ok {
		t.Fatalf("resource has no Properties: %v", resource)
	}
	return props
}

func TestTable(t *testing.T) {
	props := properties(t, only(t, template(t, StageDev), "AWS::DynamoDB::Table"))

	t.Run("uses the single-table key layout", func(t *testing.T) {
		encoded, _ := json.Marshal(props["KeySchema"])
		const want = `[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"sk","KeyType":"RANGE"}]`
		if string(encoded) != want {
			t.Errorf("KeySchema = %s, want %s", encoded, want)
		}
	})

	t.Run("bills on demand, so an idle table costs nothing", func(t *testing.T) {
		if got := props["BillingMode"]; got != "PAY_PER_REQUEST" {
			t.Errorf("BillingMode = %v, want PAY_PER_REQUEST", got)
		}
	})

	t.Run("expires items through the attribute internal/store writes", func(t *testing.T) {
		ttl, ok := props["TimeToLiveSpecification"].(map[string]any)
		if !ok {
			t.Fatalf("no TimeToLiveSpecification: %v", props)
		}
		if ttl["AttributeName"] != "expiresAt" || ttl["Enabled"] != true {
			t.Errorf("TTL = %v, want expiresAt enabled", ttl)
		}
	})

	t.Run("has no secondary index, because nothing scans", func(t *testing.T) {
		if got, found := props["GlobalSecondaryIndexes"]; found {
			t.Errorf("GlobalSecondaryIndexes = %v, want none", got)
		}
	})
}

func TestUploadBucket(t *testing.T) {
	tpl := template(t, StageDev)
	props := properties(t, only(t, tpl, "AWS::S3::Bucket"))

	t.Run("blocks every form of public access", func(t *testing.T) {
		block, ok := props["PublicAccessBlockConfiguration"].(map[string]any)
		if !ok {
			t.Fatalf("no PublicAccessBlockConfiguration: %v", props)
		}
		for _, key := range []string{"BlockPublicAcls", "BlockPublicPolicy", "IgnorePublicAcls", "RestrictPublicBuckets"} {
			if block[key] != true {
				t.Errorf("%s = %v, want true", key, block[key])
			}
		}
	})

	t.Run("encrypts at rest", func(t *testing.T) {
		encoded, _ := json.Marshal(props["BucketEncryption"])
		if !strings.Contains(string(encoded), `"SSEAlgorithm":"AES256"`) {
			t.Errorf("BucketEncryption = %s, want AES256", encoded)
		}
	})

	t.Run("expires uploads with the job that owns them", func(t *testing.T) {
		encoded, _ := json.Marshal(props["LifecycleConfiguration"])
		if !strings.Contains(string(encoded), `"ExpirationInDays":7`) {
			t.Errorf("LifecycleConfiguration = %s, want a 7 day expiry", encoded)
		}
	})

	t.Run("allows only POST from a browser, which is what a presigned post needs", func(t *testing.T) {
		encoded, _ := json.Marshal(props["CorsConfiguration"])
		if !strings.Contains(string(encoded), `"AllowedMethods":["POST"]`) {
			t.Errorf("CorsConfiguration = %s, want POST only", encoded)
		}
	})

	t.Run("refuses plaintext requests", func(t *testing.T) {
		policy := properties(t, only(t, tpl, "AWS::S3::BucketPolicy"))
		encoded, _ := json.Marshal(policy["PolicyDocument"])
		if !strings.Contains(string(encoded), `"aws:SecureTransport":"false"`) {
			t.Errorf("bucket policy does not deny plaintext: %s", encoded)
		}
	})
}

func TestFunctions(t *testing.T) {
	tpl := template(t, StageDev)

	t.Run("runs both handlers on arm64 with the provided runtime", func(t *testing.T) {
		found := resources(t, tpl, "AWS::Lambda::Function")
		// Five handlers, plus two helpers CDK adds: the one that delivers
		// bucket notifications and, in dev only, the one that empties the
		// bucket on teardown.
		if len(found) != 7 {
			t.Fatalf("found %d functions, want 7", len(found))
		}
		handlers := 0
		for _, fn := range found {
			props := properties(t, fn)
			if props["Runtime"] != "provided.al2023" {
				continue
			}
			handlers++
			encoded, _ := json.Marshal(props["Architectures"])
			if string(encoded) != `["arm64"]` {
				t.Errorf("Architectures = %s, want arm64", encoded)
			}
			if props["Handler"] != "bootstrap" {
				t.Errorf("Handler = %v, want bootstrap", props["Handler"])
			}
		}
		if handlers != 5 {
			t.Errorf("found %d provided.al2023 handlers, want 5", handlers)
		}
	})

	t.Run("expires its logs, which otherwise accrue forever", func(t *testing.T) {
		groups := resources(t, tpl, "AWS::Logs::LogGroup")
		// One per handler, plus the workflow's own.
		if len(groups) != 6 {
			t.Fatalf("found %d log groups, want 6", len(groups))
		}
		for name, group := range groups {
			if got := properties(t, group)["RetentionInDays"]; got != float64(7) {
				t.Errorf("%s retention = %v, want 7", name, got)
			}
		}
	})
}

// statements returns every inline IAM policy statement in the template.
func statements(t *testing.T, tpl assertions.Template) []map[string]any {
	t.Helper()
	var all []map[string]any
	for _, policy := range resources(t, tpl, "AWS::IAM::Policy") {
		document, ok := properties(t, policy)["PolicyDocument"].(map[string]any)
		if !ok {
			continue
		}
		list, ok := document["Statement"].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			if statement, ok := item.(map[string]any); ok {
				all = append(all, statement)
			}
		}
	}
	return all
}

// isLogDelivery reports whether a statement is the one Step Functions needs
// to deliver its execution logs.
//
// Those actions are account-level and CloudWatch accepts no resource other
// than "*" for them, so this is the single exception the wildcard check
// allows. It is narrow on purpose: it matches only when every action in the
// statement is a log-delivery one.
func isLogDelivery(statement map[string]any) bool {
	actions := asList(statement["Action"])
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if !strings.HasPrefix(action, "logs:") {
			return false
		}
	}
	return true
}

// asList normalizes a policy field that may be a string or a list.
func asList(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var out []string
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func TestPermissions(t *testing.T) {
	tpl := template(t, StageDev)
	all := statements(t, tpl)

	t.Run("lets create-job write a job", func(t *testing.T) {
		for _, statement := range all {
			if slices.Contains(asList(statement["Action"]), "dynamodb:PutItem") {
				return
			}
		}
		t.Error("no statement grants dynamodb:PutItem")
	})

	t.Run("lets create-job write only under the uploads prefix", func(t *testing.T) {
		var writes []map[string]any
		for _, statement := range all {
			if slices.Contains(asList(statement["Action"]), "s3:PutObject") {
				writes = append(writes, statement)
			}
		}
		if len(writes) != 1 {
			t.Fatalf("found %d s3:PutObject statements, want 1", len(writes))
		}
		// The resource is a Fn::Join around the bucket ARN; the prefix is the
		// part worth pinning down.
		encoded, _ := json.Marshal(writes[0]["Resource"])
		if !strings.Contains(string(encoded), "/uploads/*") {
			t.Errorf("s3:PutObject is not scoped to uploads/*: %s", encoded)
		}
	})

	t.Run("lets get-job read", func(t *testing.T) {
		for _, statement := range all {
			actions := asList(statement["Action"])
			if slices.Contains(actions, "dynamodb:GetItem") && slices.Contains(actions, "dynamodb:Query") {
				return
			}
		}
		t.Error("no statement grants dynamodb:GetItem and dynamodb:Query")
	})

	// Reading a job and writing a step result is one role's work, so the
	// pair is expected. What must never meet is the ability to read a
	// customer's document and the ability to finish their job.
	t.Run("nothing can both read a document and finish a job", func(t *testing.T) {
		for _, statement := range all {
			actions := asList(statement["Action"])
			if slices.Contains(actions, "s3:GetObject") && slices.Contains(actions, "dynamodb:UpdateItem") {
				t.Errorf("one statement grants both, so two roles were merged: %v", statement)
			}
		}
	})

	// Checked against prod, which has no auto-delete helper: that dev-only
	// custom resource legitimately needs broader S3 verbs to empty the bucket.
	t.Run("grants no wildcard action or resource in production", func(t *testing.T) {
		for _, statement := range statements(t, template(t, StageProd)) {
			if isLogDelivery(statement) {
				continue
			}
			for _, action := range asList(statement["Action"]) {
				if strings.Contains(action, "*") {
					t.Errorf("wildcard action %q in %v", action, statement)
				}
			}
			if slices.Contains(asList(statement["Resource"]), "*") {
				t.Errorf("wildcard resource in %v", statement)
			}
		}
	})
}

func TestStages(t *testing.T) {
	tests := []struct {
		name          string
		stage         Stage
		wantPolicy    string
		wantFunctions int
		wantLogLevel  string
	}{
		{name: "dev throws its data away", stage: StageDev, wantPolicy: "Delete", wantFunctions: 7, wantLogLevel: "debug"},
		{name: "prod keeps its data", stage: StageProd, wantPolicy: "Retain", wantFunctions: 6, wantLogLevel: "info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl := template(t, tt.stage)

			for _, resourceType := range []string{"AWS::DynamoDB::Table", "AWS::S3::Bucket"} {
				resource := only(t, tpl, resourceType)
				if got := resource["DeletionPolicy"]; got != tt.wantPolicy {
					t.Errorf("%s DeletionPolicy = %v, want %s", resourceType, got, tt.wantPolicy)
				}
			}

			// Production has no bucket-emptying helper.
			if found := resources(t, tpl, "AWS::Lambda::Function"); len(found) != tt.wantFunctions {
				t.Errorf("found %d functions, want %d", len(found), tt.wantFunctions)
			}

			for _, fn := range resources(t, tpl, "AWS::Lambda::Function") {
				props := properties(t, fn)
				if props["Runtime"] != "provided.al2023" {
					continue
				}
				encoded, _ := json.Marshal(props["Environment"])
				if !strings.Contains(string(encoded), `"LOG_LEVEL":"`+tt.wantLogLevel+`"`) {
					t.Errorf("environment = %s, want LOG_LEVEL %s", encoded, tt.wantLogLevel)
				}
			}
		})
	}
}

func TestCreatesNothingThatRunsWhenIdle(t *testing.T) {
	tpl := template(t, StageProd)
	for _, resourceType := range []string{
		"AWS::EC2::NatGateway",
		"AWS::EC2::VPC",
		"AWS::RDS::DBInstance",
		"AWS::OpenSearchService::Domain",
		"AWS::ElastiCache::CacheCluster",
		"AWS::ECS::Service",
	} {
		if found := resources(t, tpl, resourceType); len(found) != 0 {
			t.Errorf("stack creates %d %s, want none: it would bill while idle", len(found), resourceType)
		}
	}
}

func TestStageFromContext(t *testing.T) {
	tests := []struct {
		name    string
		context any
		want    Stage
		wantErr bool
	}{
		{name: "unset defaults to dev", context: nil, want: StageDev},
		{name: "dev", context: "dev", want: StageDev},
		{name: "prod", context: "prod", want: StageProd},
		{name: "unknown", context: "staging", wantErr: true},
		{name: "wrong type", context: 7, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var context *map[string]any
			if tt.context != nil {
				context = &map[string]any{"stage": tt.context}
			}
			app := awscdk.NewApp(&awscdk.AppProps{Context: context})

			got, err := stageFrom(app)
			if (err != nil) != tt.wantErr {
				t.Fatalf("stageFrom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("stageFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOriginsFromContext(t *testing.T) {
	tests := []struct {
		name    string
		context any
		want    []string
	}{
		{name: "unset", context: nil, want: nil},
		{name: "one origin", context: "https://a.test", want: []string{"https://a.test"}},
		{name: "comma separated", context: "https://a.test, https://b.test", want: []string{"https://a.test", "https://b.test"}},
		{name: "empty string", context: "", want: nil},
		{name: "list", context: []any{"https://a.test"}, want: []string{"https://a.test"}},
		{name: "wrong type", context: 7, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var context *map[string]any
			if tt.context != nil {
				context = &map[string]any{"uploadOrigins": tt.context}
			}
			app := awscdk.NewApp(&awscdk.AppProps{Context: context})

			got := originsFrom(app)
			if len(got) != len(tt.want) {
				t.Fatalf("originsFrom() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("origin %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestUploadOriginsDefaultsToAny(t *testing.T) {
	if got := uploadOrigins(nil); len(got) != 1 || got[0] != "*" {
		t.Errorf("uploadOrigins(nil) = %v, want [*]", got)
	}
	if got := uploadOrigins([]string{"https://a.test"}); len(got) != 1 || got[0] != "https://a.test" {
		t.Errorf("uploadOrigins() dropped the configured origin: %v", got)
	}
}

func TestEnvOrNil(t *testing.T) {
	// jsii.String("") is a pointer to an empty string, which the CDK reads as
	// an account named "" and rejects with `aws:///<region>`. Only a nil
	// leaves the stack environment-agnostic.
	tests := []struct {
		name  string
		value string
		set   bool
		want  *string
	}{
		{name: "unset", set: false, want: nil},
		{name: "empty", value: "", set: true, want: nil},
		{name: "set", value: "123456789012", set: true, want: jsii.String("123456789012")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "DOCFLOW_TEST_ENV"
			if tt.set {
				t.Setenv(key, tt.value)
			}

			got := envOrNil(key)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("envOrNil() = %q, want nil", *got)
			case tt.want != nil && got == nil:
				t.Errorf("envOrNil() = nil, want %q", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("envOrNil() = %q, want %q", *got, *tt.want)
			}
		})
	}
}

func TestQueue(t *testing.T) {
	tpl := template(t, StageDev)

	t.Run("gives up into a dead-letter queue", func(t *testing.T) {
		queues := resources(t, tpl, "AWS::SQS::Queue")
		if len(queues) != 2 {
			t.Fatalf("found %d queues, want the main one and its dead letters", len(queues))
		}
		var withRedrive int
		for _, queue := range queues {
			props := properties(t, queue)
			policy, ok := props["RedrivePolicy"].(map[string]any)
			if !ok {
				continue
			}
			withRedrive++
			// Three attempts, then stop: a message that has failed three
			// times will fail the fourth, and redelivering it forever keeps
			// the queue busy for nothing.
			if policy["maxReceiveCount"] != float64(3) {
				t.Errorf("maxReceiveCount = %v, want 3", policy["maxReceiveCount"])
			}
		}
		if withRedrive != 1 {
			t.Errorf("%d queues have a redrive policy, want exactly the main one", withRedrive)
		}
	})

	t.Run("hides a message for longer than the starter can run", func(t *testing.T) {
		// Otherwise SQS hands the same upload to a second invocation while
		// the first is still working on it.
		var timeout, functionTimeout float64
		for _, queue := range resources(t, tpl, "AWS::SQS::Queue") {
			props := properties(t, queue)
			if _, isMain := props["RedrivePolicy"]; !isMain {
				continue
			}
			timeout, _ = props["VisibilityTimeout"].(float64)
		}
		for _, fn := range resources(t, tpl, "AWS::Lambda::Function") {
			props := properties(t, fn)
			// CDK's own helper functions default to fifteen minutes and
			// never read this queue, so only this project's handlers count.
			if props["Runtime"] != "provided.al2023" {
				continue
			}
			if value, ok := props["Timeout"].(float64); ok && value > functionTimeout {
				functionTimeout = value
			}
		}
		if timeout <= functionTimeout {
			t.Errorf("visibility timeout %v is not longer than the longest function timeout %v", timeout, functionTimeout)
		}
	})

	t.Run("reacts only to uploads", func(t *testing.T) {
		// The pipeline writes its own derived artifacts into this bucket.
		// Reacting to those would start a workflow for something it just
		// produced.
		encoded, _ := json.Marshal(resources(t, tpl, "Custom::S3BucketNotifications"))
		if !strings.Contains(string(encoded), `"Name":"prefix","Value":"uploads/"`) {
			t.Errorf("the notification is not filtered to uploads/: %s", encoded)
		}
	})

	t.Run("reports partial batch failures", func(t *testing.T) {
		// One bad message must not force SQS to redeliver the good ones
		// beside it.
		mappings := resources(t, tpl, "AWS::Lambda::EventSourceMapping")
		if len(mappings) != 1 {
			t.Fatalf("found %d event source mappings, want 1", len(mappings))
		}
		for _, mapping := range mappings {
			encoded, _ := json.Marshal(properties(t, mapping)["FunctionResponseTypes"])
			if !strings.Contains(string(encoded), "ReportBatchItemFailures") {
				t.Errorf("FunctionResponseTypes = %s, want ReportBatchItemFailures", encoded)
			}
		}
	})
}

func TestWorkflow(t *testing.T) {
	tpl := template(t, StageDev)
	machine := properties(t, only(t, tpl, "AWS::StepFunctions::StateMachine"))
	definition, _ := json.Marshal(machine["DefinitionString"])
	asl := string(definition)

	t.Run("is a standard workflow", func(t *testing.T) {
		// Express bills for duration whether or not anything is happening,
		// and these executions are long and rare.
		if got := machine["StateMachineType"]; got != "STANDARD" {
			t.Errorf("StateMachineType = %v, want STANDARD", got)
		}
	})

	t.Run("retries only what is worth retrying", func(t *testing.T) {
		if !strings.Contains(asl, transientErrorName) {
			t.Errorf("no retry rule names %s: %s", transientErrorName, asl)
		}
		// A permanent failure must fall straight through to the catch. If
		// it appeared in a retry rule, a malformed upload would occupy the
		// workflow for a minute before failing.
		if strings.Contains(asl, `\"Retry\":[{\"ErrorEquals\":[\"`+permanentErrorName) {
			t.Errorf("a retry rule names %s: %s", permanentErrorName, asl)
		}
	})

	t.Run("catches everything and marks the job failed", func(t *testing.T) {
		if !strings.Contains(asl, "States.ALL") {
			t.Errorf("no catch-all: %s", asl)
		}
		if !strings.Contains(asl, "MarkFailed") {
			t.Errorf("nothing routes to MarkFailed: %s", asl)
		}
	})

	t.Run("stays short, because transitions are the billed unit", func(t *testing.T) {
		// Step Functions Standard allows four thousand transitions a month
		// on the free tier. Each state here is one per job.
		for _, state := range []string{"Validate", "MarkDone", "MarkFailed"} {
			if !strings.Contains(asl, state) {
				t.Errorf("state %s is missing: %s", state, asl)
			}
		}
		if count := strings.Count(asl, `\"Type\":\"Task\"`); count != 3 {
			t.Errorf("the workflow has %d task states, want 3", count)
		}
	})
}

func TestWorkflowPermissions(t *testing.T) {
	all := statements(t, template(t, StageDev))

	t.Run("only the starter may launch an execution", func(t *testing.T) {
		var launchers int
		for _, statement := range all {
			if slices.Contains(asList(statement["Action"]), "states:StartExecution") {
				launchers++
			}
		}
		if launchers != 1 {
			t.Errorf("%d statements grant states:StartExecution, want 1", launchers)
		}
	})

	t.Run("nothing but the finish step may write a job status", func(t *testing.T) {
		// UpdateItem is how a status changes. The starter needs it to claim
		// a job; everything else must not be able to finish one.
		var updaters int
		for _, statement := range all {
			if slices.Contains(asList(statement["Action"]), "dynamodb:UpdateItem") {
				updaters++
			}
		}
		if updaters != 2 {
			t.Errorf("%d statements grant dynamodb:UpdateItem, want the starter and the finish step", updaters)
		}
	})

	t.Run("only validate reads an uploaded document", func(t *testing.T) {
		var readers int
		for _, statement := range all {
			if slices.Contains(asList(statement["Action"]), "s3:GetObject") {
				readers++
			}
		}
		if readers != 1 {
			t.Errorf("%d statements grant s3:GetObject, want only validate", readers)
		}
	})
}

func TestUserPool(t *testing.T) {
	props := properties(t, only(t, template(t, StageDev), "AWS::Cognito::UserPool"))

	t.Run("stays on the cheapest plan that signs a token", func(t *testing.T) {
		// AWS defaults a new pool to Essentials. Both plans are free below
		// ten thousand monthly active users, but the rate beyond differs by
		// about three times, and nothing here uses what Essentials adds.
		if got := props["UserPoolTier"]; got != "LITE" {
			t.Errorf("UserPoolTier = %v, want LITE", got)
		}
	})

	t.Run("does not let the internet create accounts", func(t *testing.T) {
		admin, ok := props["AdminCreateUserConfig"].(map[string]any)
		if !ok {
			t.Fatalf("no AdminCreateUserConfig: %v", props)
		}
		if admin["AllowAdminCreateUserOnly"] != true {
			t.Error("self sign-up is on, so anyone could mint themselves a user id")
		}
	})

	t.Run("asks for a password worth having", func(t *testing.T) {
		encoded, _ := json.Marshal(props["Policies"])
		for _, want := range []string{`"MinimumLength":12`, `"RequireNumbers":true`, `"RequireSymbols":true`} {
			if !strings.Contains(string(encoded), want) {
				t.Errorf("password policy %s is missing %s", encoded, want)
			}
		}
	})
}

func TestUserPoolClient(t *testing.T) {
	t.Run("holds no secret, because its clients cannot", func(t *testing.T) {
		props := properties(t, only(t, template(t, StageDev), "AWS::Cognito::UserPoolClient"))
		if props["GenerateSecret"] == true {
			t.Error("the client has a secret, which a browser cannot keep")
		}
	})

	t.Run("allows plain passwords in dev only", func(t *testing.T) {
		// It is the difference between getting a token with one CLI call
		// and standing up a login page first, which is worth it in dev and
		// not in production.
		tests := []struct {
			stage Stage
			want  bool
		}{
			{stage: StageDev, want: true},
			{stage: StageProd, want: false},
		}
		for _, tt := range tests {
			t.Run(string(tt.stage), func(t *testing.T) {
				props := properties(t, only(t, template(t, tt.stage), "AWS::Cognito::UserPoolClient"))
				flows := asList(props["ExplicitAuthFlows"])
				got := slices.Contains(flows, "ALLOW_USER_PASSWORD_AUTH")
				if got != tt.want {
					t.Errorf("ALLOW_USER_PASSWORD_AUTH = %v in %s, want %v (flows: %v)", got, tt.stage, tt.want, flows)
				}
				if !slices.Contains(flows, "ALLOW_USER_SRP_AUTH") {
					t.Errorf("SRP is not enabled, so a real client cannot sign in: %v", flows)
				}
			})
		}
	})
}

func TestApi(t *testing.T) {
	tpl := template(t, StageDev)

	t.Run("is an HTTP API, not a REST one", func(t *testing.T) {
		// About a third of the price, and nothing here needs what the REST
		// flavour charges for.
		props := properties(t, only(t, tpl, "AWS::ApiGatewayV2::Api"))
		if got := props["ProtocolType"]; got != "HTTP" {
			t.Errorf("ProtocolType = %v, want HTTP", got)
		}
	})

	t.Run("trusts only this pool's tokens, for this client", func(t *testing.T) {
		props := properties(t, only(t, tpl, "AWS::ApiGatewayV2::Authorizer"))
		if got := props["AuthorizerType"]; got != "JWT" {
			t.Errorf("AuthorizerType = %v, want JWT", got)
		}
		encoded, _ := json.Marshal(props["JwtConfiguration"])
		// Without an audience, a token minted for any other client of the
		// same pool would be accepted.
		if !strings.Contains(string(encoded), "Audience") {
			t.Errorf("no audience is pinned: %s", encoded)
		}
		if !strings.Contains(string(encoded), "cognito-idp") {
			t.Errorf("the issuer is not this pool: %s", encoded)
		}
	})

	// The one that matters. A route added without an authorizer is open to
	// the internet, and the handlers behind these two read and write
	// whatever user the token names.
	t.Run("leaves no route open", func(t *testing.T) {
		routes := resources(t, tpl, "AWS::ApiGatewayV2::Route")
		if len(routes) != 2 {
			t.Fatalf("found %d routes, want 2", len(routes))
		}
		for name, route := range routes {
			props := properties(t, route)
			if got := props["AuthorizationType"]; got != "JWT" {
				t.Errorf("route %s (%v) has AuthorizationType %v, want JWT", name, props["RouteKey"], got)
			}
			if props["AuthorizerId"] == nil {
				t.Errorf("route %s (%v) names no authorizer", name, props["RouteKey"])
			}
		}
	})

	t.Run("exposes the two routes the handlers serve", func(t *testing.T) {
		var keys []string
		for _, route := range resources(t, tpl, "AWS::ApiGatewayV2::Route") {
			if key, ok := properties(t, route)["RouteKey"].(string); ok {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		want := []string{"GET /jobs/{id}", "POST /jobs"}
		if !slices.Equal(keys, want) {
			t.Errorf("routes = %v, want %v", keys, want)
		}
	})
}

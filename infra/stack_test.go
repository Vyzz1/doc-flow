package main

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

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
		Stage: stage,
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
		// Two handlers plus the dev-only helper that empties the bucket.
		if len(found) != 3 {
			t.Fatalf("found %d functions, want 3", len(found))
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
		if handlers != 2 {
			t.Errorf("found %d provided.al2023 handlers, want 2", handlers)
		}
	})

	t.Run("expires its logs, which otherwise accrue forever", func(t *testing.T) {
		groups := resources(t, tpl, "AWS::Logs::LogGroup")
		if len(groups) != 2 {
			t.Fatalf("found %d log groups, want 2", len(groups))
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

	t.Run("never grants reading and writing in one statement", func(t *testing.T) {
		for _, statement := range all {
			actions := asList(statement["Action"])
			if slices.Contains(actions, "dynamodb:GetItem") && slices.Contains(actions, "dynamodb:PutItem") {
				t.Errorf("one statement grants both reads and writes, so the roles were merged: %v", statement)
			}
		}
	})

	// Checked against prod, which has no auto-delete helper: that dev-only
	// custom resource legitimately needs broader S3 verbs to empty the bucket.
	t.Run("grants no wildcard action or resource in production", func(t *testing.T) {
		for _, statement := range statements(t, template(t, StageProd)) {
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
		{name: "dev throws its data away", stage: StageDev, wantPolicy: "Delete", wantFunctions: 3, wantLogLevel: "debug"},
		{name: "prod keeps its data", stage: StageProd, wantPolicy: "Retain", wantFunctions: 2, wantLogLevel: "info"},
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

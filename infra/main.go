package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	// jsii runs the CDK's JavaScript in a Node subprocess; this shuts it down.
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	stage, err := stageFrom(app)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	NewDocFlowStack(app, fmt.Sprintf("DocFlow-%s", stage), &DocFlowStackProps{
		StackProps: awscdk.StackProps{
			// The account and region come from the ambient AWS configuration,
			// so no account id is committed here.
			Env: &awscdk.Environment{
				Account: envOrNil("CDK_DEFAULT_ACCOUNT"),
				Region:  envOrNil("CDK_DEFAULT_REGION"),
			},
			Tags: &map[string]*string{
				"project": jsii.String("docflow"),
				"stage":   jsii.String(string(stage)),
			},
		},
		Stage:         stage,
		UploadOrigins: originsFrom(app),
	})

	app.Synth(nil)
}

// envOrNil returns a pointer to an environment variable, or nil when it is
// unset or empty.
//
// jsii.String("") is a pointer to an empty string, not nil, and the CDK reads
// that as an account named "", failing with `aws:///<region>`. Leaving the
// field nil is what makes the stack environment-agnostic, which is what a
// synth without credentials needs.
func envOrNil(name string) *string {
	if value := os.Getenv(name); value != "" {
		return jsii.String(value)
	}
	return nil
}

// stageFrom reads `-c stage=`. Dev is the default, so an accidental synth
// never looks like the production stack.
func stageFrom(app awscdk.App) (Stage, error) {
	raw := app.Node().TryGetContext(jsii.String("stage"))
	if raw == nil {
		return StageDev, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("context stage is %T, want a string", raw)
	}
	stage := Stage(text)
	if !stage.Valid() {
		return "", fmt.Errorf("unknown stage %q, want %s or %s", text, StageDev, StageProd)
	}
	return stage, nil
}

// originsFrom reads `-c uploadOrigins=a,b`, which the CDK passes through as a
// list when set in cdk.json and as a string on the command line.
func originsFrom(app awscdk.App) []string {
	raw := app.Node().TryGetContext(jsii.String("uploadOrigins"))
	switch value := raw.(type) {
	case nil:
		return nil
	case string:
		return splitOrigins(value)
	case []any:
		origins := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				origins = append(origins, text)
			}
		}
		return origins
	default:
		return nil
	}
}

func splitOrigins(value string) []string {
	var origins []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			origins = append(origins, part)
		}
	}
	return origins
}

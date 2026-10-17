package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
)

// mustJSON renders a template fragment so a test can look inside it.
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func TestRecognitionIsAContainer(t *testing.T) {
	tpl := template(t, StageDev)

	var image map[string]any
	var images int
	for _, fn := range resources(t, tpl, "AWS::Lambda::Function") {
		props := properties(t, fn)
		if props["PackageType"] == "Image" {
			image = props
			images++
		}
	}
	if image == nil {
		t.Fatal("no container function: recognition needs tesseract and poppler, which no managed runtime carries")
	}

	t.Run("runs on arm64 like the rest", func(t *testing.T) {
		if got := string(mustJSON(t, image["Architectures"])); got != `["arm64"]` {
			t.Errorf("Architectures = %s, want arm64", got)
		}
	})

	t.Run("gets the memory and the time OCR needs", func(t *testing.T) {
		// Lambda scales CPU with memory, and OCR dominates the pipeline's
		// runtime, so this is the one function worth paying for.
		if memory, _ := image["MemorySize"].(float64); memory < 1024 {
			t.Errorf("MemorySize = %v, want at least 1024 for OCR", image["MemorySize"])
		}
		if timeout, _ := image["Timeout"].(float64); timeout < 60 {
			t.Errorf("Timeout = %v, want at least 60s: a multi-page PDF is not quick", image["Timeout"])
		}
	})

	t.Run("is the only container", func(t *testing.T) {
		// Every other step starts faster and ships smaller as a binary.
		if images != 1 {
			t.Errorf("found %d container functions, want 1", images)
		}
	})
}

func TestWorkflowStates(t *testing.T) {
	tpl := template(t, StageDev)
	asl := workflowASL(t, tpl)

	t.Run("is a standard workflow", func(t *testing.T) {
		// Express bills for duration whether or not anything is happening,
		// and these executions are long and rare.
		machine := properties(t, only(t, tpl, "AWS::StepFunctions::StateMachine"))
		if got := machine["StateMachineType"]; got != "STANDARD" {
			t.Errorf("StateMachineType = %v, want STANDARD", got)
		}
	})

	t.Run("runs the steps in order", func(t *testing.T) {
		if asl.StartAt != "Validate" {
			t.Errorf("StartAt = %q, want Validate", asl.StartAt)
		}
		for from, want := range map[string]string{
			"Validate":  "Recognize",
			"Recognize": "Extract",
			"Extract":   "MarkDone",
		} {
			if got := asl.States[from].Next; got != want {
				t.Errorf("%s goes to %q, want %q", from, got, want)
			}
		}
	})

	// Without a catch, a state's failure ends the execution with the job
	// still PROCESSING, and nothing would ever move it: it would sit there
	// until its TTL removed it.
	t.Run("every state that can fail routes the failure somewhere", func(t *testing.T) {
		for _, state := range []string{"Validate", "Recognize", "Extract", "MarkDone"} {
			if len(asl.States[state].Catch) == 0 {
				t.Errorf("%s has no catch, so its failure would leave the job stuck PROCESSING", state)
			}
		}
		// MarkFailed is the end of that road. If marking a job failed
		// itself fails, the failed execution is what says so.
		if len(asl.States["MarkFailed"].Catch) != 0 {
			t.Error("MarkFailed has a catch, which can only loop")
		}
	})

	t.Run("retries only what is worth retrying", func(t *testing.T) {
		for name, state := range asl.States {
			var namesTransient bool
			for _, rule := range state.Retry {
				errors := rule["ErrorEquals"]
				rendered := string(mustJSON(t, errors))
				if strings.Contains(rendered, transientErrorName) {
					namesTransient = true
				}
				// A permanent failure must fall straight through to the
				// catch. In a retry rule it would occupy the workflow for a
				// minute before failing.
				if strings.Contains(rendered, permanentErrorName) {
					t.Errorf("%s retries %s", name, permanentErrorName)
				}
			}
			if !namesTransient {
				t.Errorf("%s has no retry for %s", name, transientErrorName)
			}
		}
	})

	t.Run("stays within the transition budget", func(t *testing.T) {
		// Step Functions Standard allows four thousand transitions a month
		// on the free tier, and each state is one per job.
		if len(asl.States) != 5 {
			t.Errorf("the workflow has %d states, want 5", len(asl.States))
		}
	})
}

// aslDefinition is the part of a state machine definition these tests read.
type aslDefinition struct {
	StartAt string `json:"StartAt"`
	States  map[string]struct {
		Next  string           `json:"Next"`
		Retry []map[string]any `json:"Retry"`
		Catch []map[string]any `json:"Catch"`
	} `json:"States"`
}

// workflowASL resolves the definition's CloudFormation intrinsics and
// decodes it.
//
// Reading the definition the CDK actually synthesized, rather than a copy
// kept beside it, is what makes these assertions worth anything.
func workflowASL(t *testing.T, tpl assertions.Template) aslDefinition {
	t.Helper()
	machine := properties(t, only(t, tpl, "AWS::StepFunctions::StateMachine"))
	joined, ok := machine["DefinitionString"].(map[string]any)
	if !ok {
		t.Fatalf("DefinitionString is %T, want an Fn::Join", machine["DefinitionString"])
	}
	parts, ok := joined["Fn::Join"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("DefinitionString is not a two-part Fn::Join: %v", joined)
	}
	pieces, ok := parts[1].([]any)
	if !ok {
		t.Fatalf("Fn::Join has no list of pieces: %v", parts[1])
	}

	var builder strings.Builder
	for _, piece := range pieces {
		if text, isText := piece.(string); isText {
			builder.WriteString(text)
			continue
		}
		// A reference to a function's ARN, which these assertions do not
		// care about.
		builder.WriteString("arn:aws:lambda:ap-southeast-1:123456789012:function:Fake")
	}

	var asl aslDefinition
	if err := json.Unmarshal([]byte(builder.String()), &asl); err != nil {
		t.Fatalf("the definition is not valid JSON: %v\n%s", err, builder.String())
	}
	return asl
}

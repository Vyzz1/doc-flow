package store

import (
	"fmt"
	"sort"
	"time"

	"docflow/internal/model"
)

// Key prefixes and the sort key of the single result item. They are part of
// the table contract in AGENTS.md.
const (
	userPrefix = "USER#"
	jobPrefix  = "JOB#"
	stepPrefix = "STEP#"
	resultSK   = "RESULT"
)

// Attribute names. ttlAttr is the one the table's TTL setting must name.
const (
	pkAttr     = "pk"
	skAttr     = "sk"
	statusAttr = "status"
	ttlAttr    = "expiresAt"
)

func userPK(userID string) string       { return userPrefix + userID }
func jobSK(jobID string) string         { return jobPrefix + jobID }
func jobPK(jobID string) string         { return jobPrefix + jobID }
func stepSK(step model.StepName) string { return stepPrefix + string(step) }
func unixSeconds(t time.Time) int64     { return t.UTC().Unix() }
func formatTime(t time.Time) string     { return t.UTC().Format(time.RFC3339Nano) }
func expiryFrom(now time.Time) int64    { return unixSeconds(now.Add(model.Retention)) }
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// jobItem is a job as the table stores it. The model type stays free of
// storage tags, so the key layout can change without touching the domain.
type jobItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	UserID      string `dynamodbav:"userId"`
	JobID       string `dynamodbav:"jobId"`
	Status      string `dynamodbav:"status"`
	DocType     string `dynamodbav:"docType"`
	UploadKey   string `dynamodbav:"uploadKey"`
	ContentType string `dynamodbav:"contentType,omitempty"`
	SizeBytes   int64  `dynamodbav:"sizeBytes"`

	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	ExpiresAt int64  `dynamodbav:"expiresAt"`

	FailureReason string `dynamodbav:"failureReason,omitempty"`
}

func newJobItem(job model.Job) jobItem {
	return jobItem{
		PK:            userPK(job.UserID),
		SK:            jobSK(job.JobID),
		UserID:        job.UserID,
		JobID:         job.JobID,
		Status:        string(job.Status),
		DocType:       string(job.DocType),
		UploadKey:     job.UploadKey,
		ContentType:   job.ContentType,
		SizeBytes:     job.SizeBytes,
		CreatedAt:     formatTime(job.CreatedAt),
		UpdatedAt:     formatTime(job.UpdatedAt),
		ExpiresAt:     unixSeconds(job.ExpiresAt),
		FailureReason: job.FailureReason,
	}
}

func (i jobItem) toModel() (model.Job, error) {
	createdAt, err := parseTime(i.CreatedAt)
	if err != nil {
		return model.Job{}, fmt.Errorf("job %s: %w", i.JobID, err)
	}
	updatedAt, err := parseTime(i.UpdatedAt)
	if err != nil {
		return model.Job{}, fmt.Errorf("job %s: %w", i.JobID, err)
	}
	job := model.Job{
		UserID:        i.UserID,
		JobID:         i.JobID,
		Status:        model.JobStatus(i.Status),
		DocType:       model.DocType(i.DocType),
		UploadKey:     i.UploadKey,
		ContentType:   i.ContentType,
		SizeBytes:     i.SizeBytes,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		ExpiresAt:     time.Unix(i.ExpiresAt, 0).UTC(),
		FailureReason: i.FailureReason,
	}
	// A stored item that no longer satisfies the domain rules is a bug
	// somewhere upstream; fail at the boundary rather than deeper in a
	// workflow state.
	if err := job.Validate(); err != nil {
		return model.Job{}, fmt.Errorf("stored job %s: %w", i.JobID, err)
	}
	return job, nil
}

// stepItem is one workflow step's outcome.
type stepItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	JobID  string `dynamodbav:"jobId"`
	Step   string `dynamodbav:"step"`
	Status string `dynamodbav:"status"`

	StartedAt  string   `dynamodbav:"startedAt"`
	DurationMS int64    `dynamodbav:"durationMs"`
	Artifacts  []string `dynamodbav:"artifacts,omitempty"`
	Error      string   `dynamodbav:"error,omitempty"`
	Retryable  bool     `dynamodbav:"retryable"`

	ExpiresAt int64 `dynamodbav:"expiresAt"`
}

func newStepItem(r model.StepResult, expiresAt int64) stepItem {
	return stepItem{
		PK:         jobPK(r.JobID),
		SK:         stepSK(r.Step),
		JobID:      r.JobID,
		Step:       string(r.Step),
		Status:     string(r.Status),
		StartedAt:  formatTime(r.StartedAt),
		DurationMS: r.Duration.Milliseconds(),
		Artifacts:  r.Artifacts,
		Error:      r.Error,
		Retryable:  r.Retryable,
		ExpiresAt:  expiresAt,
	}
}

func (i stepItem) toModel() (model.StepResult, error) {
	startedAt, err := parseTime(i.StartedAt)
	if err != nil {
		return model.StepResult{}, fmt.Errorf("step %s of job %s: %w", i.Step, i.JobID, err)
	}
	result := model.StepResult{
		JobID:     i.JobID,
		Step:      model.StepName(i.Step),
		Status:    model.StepStatus(i.Status),
		StartedAt: startedAt,
		Duration:  time.Duration(i.DurationMS) * time.Millisecond,
		Artifacts: i.Artifacts,
		Error:     i.Error,
		Retryable: i.Retryable,
	}
	if err := result.Validate(); err != nil {
		return model.StepResult{}, fmt.Errorf("stored step %s of job %s: %w", i.Step, i.JobID, err)
	}
	return result, nil
}

// sortSteps puts step results in the order the workflow runs them, which is
// the order a progress view wants. DynamoDB returns them sorted by name.
func sortSteps(results []model.StepResult) {
	order := map[model.StepName]int{
		model.StepValidate:   0,
		model.StepPreprocess: 1,
		model.StepThumbnail:  2,
		model.StepOCR:        3,
		model.StepExtract:    4,
		model.StepSave:       5,
	}
	sort.SliceStable(results, func(a, b int) bool {
		return order[results[a].Step] < order[results[b].Step]
	})
}

// fieldItem is one extracted field inside a result item.
type fieldItem struct {
	Name       string  `dynamodbav:"name"`
	Value      string  `dynamodbav:"value"`
	Confidence float64 `dynamodbav:"confidence"`
}

// resultItem is a job's recognized text and extracted fields.
//
// The text rides along in the item, which keeps the result a single read.
// DynamoDB caps an item at 400 KB; the OCR step is responsible for not
// exceeding that, and spilling long text to S3 if it ever does.
type resultItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	JobID      string      `dynamodbav:"jobId"`
	Provider   string      `dynamodbav:"provider"`
	Confidence float64     `dynamodbav:"confidence"`
	Pages      int         `dynamodbav:"pages"`
	Text       string      `dynamodbav:"text"`
	Fields     []fieldItem `dynamodbav:"fields,omitempty"`

	ExpiresAt int64 `dynamodbav:"expiresAt"`
}

func newResultItem(r model.DocumentResult, expiresAt int64) resultItem {
	fields := make([]fieldItem, 0, len(r.Fields))
	for _, f := range r.Fields {
		fields = append(fields, fieldItem{Name: f.Name, Value: f.Value, Confidence: f.Confidence})
	}
	return resultItem{
		PK:         jobPK(r.JobID),
		SK:         resultSK,
		JobID:      r.JobID,
		Provider:   string(r.Provider),
		Confidence: r.Confidence,
		Pages:      r.Pages,
		Text:       r.Text,
		Fields:     fields,
		ExpiresAt:  expiresAt,
	}
}

func (i resultItem) toModel() (model.DocumentResult, error) {
	fields := make([]model.Field, 0, len(i.Fields))
	for _, f := range i.Fields {
		fields = append(fields, model.Field{Name: f.Name, Value: f.Value, Confidence: f.Confidence})
	}
	result := model.DocumentResult{
		JobID:      i.JobID,
		Provider:   model.OCRProvider(i.Provider),
		Confidence: i.Confidence,
		Pages:      i.Pages,
		Text:       i.Text,
	}
	if len(fields) > 0 {
		result.Fields = fields
	}
	if err := result.Validate(); err != nil {
		return model.DocumentResult{}, fmt.Errorf("stored result of job %s: %w", i.JobID, err)
	}
	return result, nil
}

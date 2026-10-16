package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/store"
)

// StepView is one workflow step as the API reports it.
type StepView struct {
	Step       string `json:"step"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// FieldView is one extracted field as the API reports it.
type FieldView struct {
	Name       string  `json:"name"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

// ResultView is a finished job's output.
type ResultView struct {
	Provider   string      `json:"provider"`
	Confidence float64     `json:"confidence"`
	Pages      int         `json:"pages"`
	Text       string      `json:"text"`
	Fields     []FieldView `json:"fields"`
}

// GetJobResponse is what a client polling a job receives.
type GetJobResponse struct {
	JobID   string `json:"jobId"`
	Status  string `json:"status"`
	DocType string `json:"docType"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	FailureReason string      `json:"failureReason,omitempty"`
	Steps         []StepView  `json:"steps"`
	Result        *ResultView `json:"result,omitempty"`
}

// GetJob reports one job's progress.
type GetJob struct {
	Jobs store.JobStore
}

// NewGetJob returns a handler reading from jobs.
func NewGetJob(jobs store.JobStore) *GetJob {
	return &GetJob{Jobs: jobs}
}

// Handle returns the job, its steps, and its result once it is DONE.
//
// Ownership needs no separate check: a job lives under its owner's partition
// key, so another user's id simply does not find it. A caller cannot learn
// whether someone else's job exists.
func (h *GetJob) Handle(ctx context.Context, r Request) (Response, error) {
	userID, err := UserID(r)
	if err != nil {
		return Fail(ctx, http.StatusUnauthorized, "unauthenticated", err)
	}
	ctx = logging.WithUserID(ctx, userID)

	jobID := r.PathParameters["id"]
	// Checked before the read: a malformed id cannot name a stored job, and
	// rejecting it here costs nothing instead of a wasted capacity unit.
	if !model.ValidJobID(jobID) {
		return Fail(ctx, http.StatusBadRequest, "job id is not a ULID", nil)
	}
	ctx = logging.WithJobID(ctx, jobID)

	job, err := h.Jobs.GetJob(ctx, userID, jobID)
	if err != nil {
		if errors.Is(err, store.ErrJobNotFound) {
			return Fail(ctx, http.StatusNotFound, "job not found", err)
		}
		return Fail(ctx, StatusFor(err), "could not read the job", err)
	}

	steps, err := h.Jobs.ListStepResults(ctx, jobID)
	if err != nil {
		return Fail(ctx, StatusFor(err), "could not read the job's progress", err)
	}

	response := GetJobResponse{
		JobID:         job.JobID,
		Status:        string(job.Status),
		DocType:       string(job.DocType),
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		FailureReason: job.FailureReason,
		Steps:         make([]StepView, 0, len(steps)),
	}
	for _, step := range steps {
		response.Steps = append(response.Steps, StepView{
			Step:       string(step.Step),
			Status:     string(step.Status),
			DurationMS: step.Duration.Milliseconds(),
			Error:      step.Error,
		})
	}

	if job.Status == model.StatusDone {
		result, err := h.Jobs.GetResult(ctx, jobID)
		switch {
		case errors.Is(err, store.ErrResultNotFound):
			// DONE without a result means the save step and the status
			// write disagree. Report the job rather than a 404: the status
			// is still true, and the gap belongs in the logs.
			logging.FromContext(ctx).ErrorContext(ctx, "job is DONE but has no stored result")
		case err != nil:
			return Fail(ctx, StatusFor(err), "could not read the job's result", err)
		default:
			response.Result = newResultView(result)
		}
	}

	return JSON(http.StatusOK, response)
}

func newResultView(result model.DocumentResult) *ResultView {
	view := &ResultView{
		Provider:   string(result.Provider),
		Confidence: result.Confidence,
		Pages:      result.Pages,
		Text:       result.Text,
		Fields:     make([]FieldView, 0, len(result.Fields)),
	}
	for _, field := range result.Fields {
		view.Fields = append(view.Fields, FieldView{
			Name:       field.Name,
			Value:      field.Value,
			Confidence: field.Confidence,
		})
	}
	return view
}

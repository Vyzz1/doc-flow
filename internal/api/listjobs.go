package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/store"
)

const (
	defaultListLimit = 20
	maxListLimit     = 50
)

// JobSummary is one job in a listing: enough to draw a row, without the
// steps and result that GET /jobs/{id} reads separately.
type JobSummary struct {
	JobID         string    `json:"jobId"`
	Status        string    `json:"status"`
	DocType       string    `json:"docType"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	FailureReason string    `json:"failureReason,omitempty"`
}

// ListJobsResponse is a page of the caller's jobs, newest first. Next is set
// only when more remain; pass it back as the next query parameter.
type ListJobsResponse struct {
	Jobs []JobSummary `json:"jobs"`
	Next string       `json:"next,omitempty"`
}

// ListJobs reports the caller's own jobs.
type ListJobs struct {
	Jobs store.JobStore
}

// NewListJobs returns a handler reading from jobs.
func NewListJobs(jobs store.JobStore) *ListJobs {
	return &ListJobs{Jobs: jobs}
}

// Handle returns one page of the caller's jobs.
//
// Like GetJob it needs no ownership check: the query is bound to the caller's
// partition key, so another user's jobs are not reachable, whatever next says.
func (h *ListJobs) Handle(ctx context.Context, r Request) (Response, error) {
	userID, err := UserID(r)
	if err != nil {
		return Fail(ctx, http.StatusUnauthorized, "unauthenticated", err)
	}
	ctx = logging.WithUserID(ctx, userID)

	limit := int32(defaultListLimit)
	if raw := r.QueryStringParameters["limit"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxListLimit {
			return Fail(ctx, http.StatusBadRequest, "limit must be a whole number from 1 to 50", err)
		}
		limit = int32(n)
	}

	next := r.QueryStringParameters["next"]
	if next != "" && !model.ValidJobID(next) {
		return Fail(ctx, http.StatusBadRequest, "next is not a job id", nil)
	}

	jobs, token, err := h.Jobs.ListJobs(ctx, userID, limit, next)
	if err != nil {
		return Fail(ctx, StatusFor(err), "could not list the jobs", err)
	}

	response := ListJobsResponse{Jobs: make([]JobSummary, 0, len(jobs)), Next: token}
	for _, job := range jobs {
		response.Jobs = append(response.Jobs, JobSummary{
			JobID:         job.JobID,
			Status:        string(job.Status),
			DocType:       string(job.DocType),
			CreatedAt:     job.CreatedAt,
			UpdatedAt:     job.UpdatedAt,
			FailureReason: job.FailureReason,
		})
	}
	return JSON(http.StatusOK, response)
}

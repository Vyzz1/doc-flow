package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"docflow/internal/model"
)

// listRequest builds an authenticated GET /jobs request with query parameters.
func listRequest(sub string, query map[string]string) Request {
	r := request(sub, "")
	r.QueryStringParameters = query
	return r
}

func TestListJobs(t *testing.T) {
	failed := storedJob(t, model.StatusFailed)
	done := storedJob(t, model.StatusDone)
	done.JobID = "01J8ZQJ9X7K3M4N5P6Q7R8S9TW"

	jobs := newFakeStore()
	jobs.listed = []model.Job{done, failed}
	jobs.nextToken = failed.JobID

	resp, err := NewListJobs(jobs).Handle(context.Background(), listRequest(testUser, nil))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", resp.StatusCode, resp.Body)
	}

	body := decode[ListJobsResponse](t, resp.Body)
	if len(body.Jobs) != 2 || body.Jobs[0].JobID != done.JobID || body.Jobs[1].JobID != failed.JobID {
		t.Fatalf("jobs = %+v, want the store's order kept", body.Jobs)
	}
	if body.Jobs[1].Status != string(model.StatusFailed) || body.Jobs[1].FailureReason != failed.FailureReason {
		t.Errorf("failed job = %+v, want its status and reason", body.Jobs[1])
	}
	if body.Next != failed.JobID {
		t.Errorf("next = %q, want %q", body.Next, failed.JobID)
	}
	if jobs.listedUser != testUser || jobs.listedLimit != defaultListLimit || jobs.listedToken != "" {
		t.Errorf("store got user %q limit %d token %q, want the caller, the default limit, no token",
			jobs.listedUser, jobs.listedLimit, jobs.listedToken)
	}
}

func TestListJobsWithNoJobsReturnsAnEmptyArray(t *testing.T) {
	resp, err := NewListJobs(newFakeStore()).Handle(context.Background(), listRequest(testUser, nil))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// [] and not null, so a client can map over it without a nil check.
	if want := `{"jobs":[]}`; resp.Body != want {
		t.Errorf("body = %s, want %s", resp.Body, want)
	}
}

func TestListJobsPassesTheQuery(t *testing.T) {
	jobs := newFakeStore()
	query := map[string]string{"limit": "50", "next": testJobID}

	resp, err := NewListJobs(jobs).Handle(context.Background(), listRequest(testUser, query))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", resp.StatusCode, resp.Body)
	}
	if jobs.listedLimit != 50 || jobs.listedToken != testJobID {
		t.Errorf("store got limit %d token %q, want 50 and %q", jobs.listedLimit, jobs.listedToken, testJobID)
	}
}

func TestListJobsRejections(t *testing.T) {
	tests := []struct {
		name     string
		sub      string
		query    map[string]string
		storeErr error
		want     int
	}{
		{name: "no identity", sub: "", want: http.StatusUnauthorized},
		{name: "limit is not a number", sub: testUser, query: map[string]string{"limit": "many"}, want: http.StatusBadRequest},
		{name: "limit is zero", sub: testUser, query: map[string]string{"limit": "0"}, want: http.StatusBadRequest},
		{name: "limit is negative", sub: testUser, query: map[string]string{"limit": "-3"}, want: http.StatusBadRequest},
		{name: "limit is over the cap", sub: testUser, query: map[string]string{"limit": "51"}, want: http.StatusBadRequest},
		{name: "next is not a ULID", sub: testUser, query: map[string]string{"next": "../etc"}, want: http.StatusBadRequest},
		{name: "store fails", sub: testUser, storeErr: errors.New("throttled"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore()
			if tt.storeErr != nil {
				jobs.fail("ListJobs", tt.storeErr)
			}

			resp, err := NewListJobs(jobs).Handle(context.Background(), listRequest(tt.sub, tt.query))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d\nbody: %s", resp.StatusCode, tt.want, resp.Body)
			}
			// A rejected request must not have reached the store with bad input.
			if tt.want == http.StatusBadRequest && jobs.listedUser != "" {
				t.Errorf("store was called for a rejected request")
			}
		})
	}
}

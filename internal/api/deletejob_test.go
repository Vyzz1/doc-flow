package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"docflow/internal/model"
	"docflow/internal/store"
)

func TestDeleteJob(t *testing.T) {
	jobs := newFakeStore()
	objects := &fakeRemover{}

	resp, err := NewDeleteJob(jobs, objects).Handle(context.Background(), getRequest(testUser, testJobID))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp.StatusCode != http.StatusNoContent || resp.Body != "" {
		t.Fatalf("response = %d %q, want 204 with no body", resp.StatusCode, resp.Body)
	}

	if want := []string{testUser + "/" + testJobID}; !slices.Equal(jobs.deleted, want) {
		t.Errorf("deleted jobs = %v, want %v", jobs.deleted, want)
	}
	if want := []string{model.UploadKey(testUser, testJobID)}; !slices.Equal(objects.keys, want) {
		t.Errorf("deleted keys = %v, want %v", objects.keys, want)
	}
	if want := []string{model.DerivedPrefix(testUser, testJobID)}; !slices.Equal(objects.prefixes, want) {
		t.Errorf("deleted prefixes = %v, want %v", objects.prefixes, want)
	}
}

func TestDeleteJobRejections(t *testing.T) {
	tests := []struct {
		name     string
		sub      string
		jobID    string
		storeErr error
		want     int
	}{
		{name: "no identity", sub: "", jobID: testJobID, want: http.StatusUnauthorized},
		{name: "id is not a ULID", sub: testUser, jobID: "../uploads", want: http.StatusBadRequest},
		{name: "no such job", sub: testUser, jobID: testJobID, storeErr: store.ErrJobNotFound, want: http.StatusNotFound},
		{name: "job is processing", sub: testUser, jobID: testJobID, storeErr: store.ErrStatusConflict, want: http.StatusConflict},
		{name: "store fails", sub: testUser, jobID: testJobID, storeErr: errors.New("throttled"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newFakeStore()
			if tt.storeErr != nil {
				jobs.fail("DeleteJob", tt.storeErr)
			}
			objects := &fakeRemover{}

			resp, err := NewDeleteJob(jobs, objects).Handle(context.Background(), getRequest(tt.sub, tt.jobID))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d\nbody: %s", resp.StatusCode, tt.want, resp.Body)
			}
			// A refused delete must leave the files alone: for a job still
			// processing they are in use.
			if len(objects.keys) != 0 || len(objects.prefixes) != 0 {
				t.Errorf("objects were deleted (%v, %v) although the job was not", objects.keys, objects.prefixes)
			}
			if tt.want == http.StatusBadRequest && len(jobs.deleted) != 0 {
				t.Error("the store was called for a rejected id")
			}
		})
	}
}

func TestDeleteJobTellsTheCallerWhenFilesRemain(t *testing.T) {
	tests := []struct {
		name    string
		objects *fakeRemover
	}{
		{name: "the upload cannot be deleted", objects: &fakeRemover{keyErr: errors.New("denied")}},
		{name: "the artifacts cannot be deleted", objects: &fakeRemover{prefixErr: errors.New("denied")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := NewDeleteJob(newFakeStore(), tt.objects).Handle(context.Background(), getRequest(testUser, testJobID))
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if resp.StatusCode != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500\nbody: %s", resp.StatusCode, resp.Body)
			}
			// One failure must not stop the other delete from being tried.
			if len(tt.objects.keys) != 1 || len(tt.objects.prefixes) != 1 {
				t.Errorf("tried %d keys and %d prefixes, want one of each", len(tt.objects.keys), len(tt.objects.prefixes))
			}
		})
	}
}

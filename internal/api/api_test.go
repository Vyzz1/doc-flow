package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"docflow/internal/model"
	"docflow/internal/store"
)

func TestStatusFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "job missing", err: store.ErrJobNotFound, want: http.StatusNotFound},
		{name: "result missing", err: store.ErrResultNotFound, want: http.StatusNotFound},
		{name: "duplicate job", err: store.ErrJobExists, want: http.StatusConflict},
		{name: "status raced", err: store.ErrStatusConflict, want: http.StatusConflict},
		{name: "no identity", err: ErrUnauthenticated, want: http.StatusUnauthorized},
		{name: "content type", err: model.ErrUnsupportedContentType, want: http.StatusBadRequest},
		{name: "doc type", err: model.ErrInvalidDocType, want: http.StatusBadRequest},
		{name: "user id", err: model.ErrInvalidUserID, want: http.StatusBadRequest},
		{name: "job id", err: model.ErrInvalidJobID, want: http.StatusBadRequest},

		// Anything unrecognized is the server's fault until shown otherwise.
		{name: "unknown", err: errors.New("throttled"), want: http.StatusInternalServerError},
		{name: "nil", err: nil, want: http.StatusInternalServerError},

		// The mapping must survive wrapping, which is how these errors
		// actually arrive.
		{name: "wrapped", err: fmt.Errorf("get job: %w", store.ErrJobNotFound), want: http.StatusNotFound},
		{name: "wrapped twice", err: fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", store.ErrJobExists)), want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusFor(tt.err); got != tt.want {
				t.Errorf("StatusFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestUserID(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		want    string
		wantErr bool
	}{
		{name: "subject present", req: request("user-1", ""), want: "user-1"},
		{name: "trimmed", req: request("  user-1  ", ""), want: "user-1"},

		{name: "no authorizer", req: Request{}, wantErr: true},
		{name: "blank subject", req: request("   ", ""), wantErr: true},
		{name: "subject with a key separator", req: request("USER#1", ""), wantErr: true},
		{name: "subject with a slash", req: request("a/b", ""), wantErr: true},
		{name: "oversized subject", req: request(strings.Repeat("u", 200), ""), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UserID(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UserID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrUnauthenticated) {
					t.Errorf("UserID() error = %v, want it to wrap ErrUnauthenticated", err)
				}
				if StatusFor(err) != http.StatusUnauthorized {
					t.Errorf("StatusFor(%v) = %d, want 401", err, StatusFor(err))
				}
				return
			}
			if got != tt.want {
				t.Errorf("UserID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserIDWithEmptyJWTClaims(t *testing.T) {
	r := request("user-1", "")
	r.RequestContext.Authorizer.JWT = nil

	if _, err := UserID(r); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("UserID() error = %v, want ErrUnauthenticated", err)
	}
}

func TestJSONReportsEncodingFailure(t *testing.T) {
	// A channel cannot be marshalled, which is the only way JSON fails.
	if _, err := JSON(http.StatusOK, make(chan int)); err == nil {
		t.Error("JSON() = nil error for an unmarshalable body, want an error")
	}
}

func TestFailKeepsTheCauseOutOfTheBody(t *testing.T) {
	resp, err := Fail(context.Background(), http.StatusInternalServerError,
		"could not create a job", errors.New("table docflow-prod: access denied"))
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if strings.Contains(resp.Body, "docflow-prod") {
		t.Errorf("response leaked the cause: %s", resp.Body)
	}
	if got := decode[errorBody](t, resp.Body); got.Error != "could not create a job" {
		t.Errorf("error = %q, want the caller's message", got.Error)
	}
}

func TestFailWithoutACause(t *testing.T) {
	resp, err := Fail(context.Background(), http.StatusBadRequest, "job id is not a ULID", nil)
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

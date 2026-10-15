package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// logOne writes one record through a fresh logger and returns it decoded.
func logOne(t *testing.T, level slog.Leveler, write func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	write(New(&buf, level))
	if buf.Len() == 0 {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("log line is not JSON: %v\nline: %s", err, buf.String())
	}
	return got
}

func TestRecordUsesLambdaFieldNames(t *testing.T) {
	got := logOne(t, slog.LevelInfo, func(l *slog.Logger) {
		l.Info("job accepted")
	})

	if got["message"] != "job accepted" {
		t.Errorf("message = %v, want %q", got["message"], "job accepted")
	}
	if got["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", got["level"])
	}
	ts, ok := got["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp = %v, want a string", got["timestamp"])
	}
	if !strings.HasSuffix(ts, "Z") || len(ts) != len("2006-01-02T15:04:05.000Z") {
		t.Errorf("timestamp = %q, want UTC with milliseconds", ts)
	}
	for _, stale := range []string{"msg", "time"} {
		if _, found := got[stale]; found {
			t.Errorf("record still carries slog's %q key", stale)
		}
	}
}

func TestContextIdentifiersLandOnEveryRecord(t *testing.T) {
	tests := []struct {
		name       string
		ctx        func() context.Context
		wantJobID  string
		wantUserID string
	}{
		{
			name:       "both",
			ctx:        func() context.Context { return WithJob(context.Background(), "job-1", "user-1") },
			wantJobID:  "job-1",
			wantUserID: "user-1",
		},
		{
			name:      "job only",
			ctx:       func() context.Context { return WithJobID(context.Background(), "job-1") },
			wantJobID: "job-1",
		},
		{
			name:       "user only",
			ctx:        func() context.Context { return WithUserID(context.Background(), "user-1") },
			wantUserID: "user-1",
		},
		{
			name: "neither",
			ctx:  context.Background,
		},
		{
			name:      "later value wins",
			ctx:       func() context.Context { return WithJobID(WithJobID(context.Background(), "old"), "new") },
			wantJobID: "new",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx()
			got := logOne(t, slog.LevelInfo, func(l *slog.Logger) {
				l.InfoContext(ctx, "step finished")
			})

			if tt.wantJobID == "" {
				if _, found := got[JobIDKey]; found {
					t.Errorf("record carries %s = %v with none in context", JobIDKey, got[JobIDKey])
				}
			} else if got[JobIDKey] != tt.wantJobID {
				t.Errorf("%s = %v, want %q", JobIDKey, got[JobIDKey], tt.wantJobID)
			}

			if tt.wantUserID == "" {
				if _, found := got[UserIDKey]; found {
					t.Errorf("record carries %s = %v with none in context", UserIDKey, got[UserIDKey])
				}
			} else if got[UserIDKey] != tt.wantUserID {
				t.Errorf("%s = %v, want %q", UserIDKey, got[UserIDKey], tt.wantUserID)
			}
		})
	}
}

func TestIdentifiersSurviveWithAttrs(t *testing.T) {
	ctx := WithJob(context.Background(), "job-1", "user-1")
	got := logOne(t, slog.LevelInfo, func(l *slog.Logger) {
		l.With(slog.String("step", "ocr")).InfoContext(ctx, "step finished")
	})

	if got[JobIDKey] != "job-1" || got[UserIDKey] != "user-1" {
		t.Errorf("identifiers lost through With: %v", got)
	}
	if got["step"] != "ocr" {
		t.Errorf("step = %v, want ocr", got["step"])
	}
}

// A grouped child logger nests the context attributes inside the group. This
// documents the behaviour so a caller is not surprised by a missing top-level
// jobId after calling WithGroup.
func TestWithGroupNestsIdentifiers(t *testing.T) {
	ctx := WithJob(context.Background(), "job-1", "user-1")
	got := logOne(t, slog.LevelInfo, func(l *slog.Logger) {
		l.WithGroup("ocr").InfoContext(ctx, "step finished")
	})

	group, ok := got["ocr"].(map[string]any)
	if !ok {
		t.Fatalf("ocr group = %v, want an object", got["ocr"])
	}
	if group[JobIDKey] != "job-1" {
		t.Errorf("grouped %s = %v, want job-1", JobIDKey, group[JobIDKey])
	}
	if _, found := got[JobIDKey]; found {
		t.Errorf("%s is also at the top level: %v", JobIDKey, got)
	}
}

func TestLevelFiltering(t *testing.T) {
	if got := logOne(t, slog.LevelWarn, func(l *slog.Logger) { l.Info("quiet") }); got != nil {
		t.Errorf("info record written at warn level: %v", got)
	}
	if got := logOne(t, slog.LevelWarn, func(l *slog.Logger) { l.Error("loud") }); got == nil {
		t.Error("error record dropped at warn level")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "", want: slog.LevelInfo},
		{in: "info", want: slog.LevelInfo},
		{in: "INFO", want: slog.LevelInfo},
		{in: "  debug  ", want: slog.LevelDebug},
		{in: "warn", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "trace", want: slog.LevelInfo, wantErr: true},
		{in: "5", want: slog.LevelInfo, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseLevel(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewFromEnvFallsBackToInfo(t *testing.T) {
	t.Setenv("LOG_LEVEL", "nonsense")
	var buf bytes.Buffer
	logger := NewFromEnv(&buf)

	if !strings.Contains(buf.String(), "unreadable LOG_LEVEL") {
		t.Errorf("no warning about the bad level: %q", buf.String())
	}
	buf.Reset()
	logger.Info("still logging")
	if !strings.Contains(buf.String(), "still logging") {
		t.Error("logger does not log at info after falling back")
	}
}

func TestNewFromEnvHonoursLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error")
	var buf bytes.Buffer
	logger := NewFromEnv(&buf)

	logger.Warn("dropped")
	if buf.Len() != 0 {
		t.Errorf("warn record written at error level: %q", buf.String())
	}
}

func TestContextLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	if got := FromContext(Into(context.Background(), logger)); got != logger {
		t.Error("FromContext did not return the logger put there by Into")
	}
	if got := FromContext(context.Background()); got != slog.Default() {
		t.Error("FromContext did not fall back to slog.Default")
	}
	// A careless caller can still pass a nil context; it must not panic.
	var missing context.Context
	if got := FromContext(missing); got != slog.Default() {
		t.Error("FromContext(nil) did not fall back to slog.Default")
	}
}

func TestIdentifiersFromNilContext(t *testing.T) {
	// JobID and UserID read the context directly, so they carry the same
	// no-panic guarantee as FromContext.
	var missing context.Context
	if got := JobID(missing); got != "" {
		t.Errorf("JobID(nil) = %q, want empty", got)
	}
	if got := UserID(missing); got != "" {
		t.Errorf("UserID(nil) = %q, want empty", got)
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "presigned upload",
			in:   "https://bucket.s3.amazonaws.com/uploads/user-1/job-1?X-Amz-Signature=deadbeef&X-Amz-Expires=900",
			want: "https://bucket.s3.amazonaws.com/uploads/user-1/job-1",
		},
		{name: "no query", in: "https://example.test/a/b", want: "https://example.test/a/b"},
		{name: "fragment", in: "https://example.test/a#frag", want: "https://example.test/a"},
		{name: "credentials in userinfo", in: "https://key:secret@example.test/a", want: "https://example.test/a"},
		{name: "empty", in: "", want: ""},
		{name: "unparseable", in: "https://exa mple.test/\x7f", want: "[unparseable url]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactURL(tt.in); got != tt.want {
				t.Errorf("RedactURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactURLDropsSignature(t *testing.T) {
	const signed = "https://bucket.s3.amazonaws.com/uploads/u/j?X-Amz-Signature=deadbeef"
	if got := RedactURL(signed); strings.Contains(got, "deadbeef") {
		t.Errorf("RedactURL kept the signature: %q", got)
	}
}

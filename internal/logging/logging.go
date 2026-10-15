// Package logging provides the pipeline's structured JSON logging.
//
// Every record carries the jobId and userId held by its context, so a handler
// that puts them there once does not have to repeat them on each call. The
// field names match what AWS Lambda's JSON log format expects (timestamp,
// level, message), which lets CloudWatch parse the level without a filter.
//
// Nothing here redacts a value you pass it. Keep document contents, OCR text,
// and credentials out of log calls, and pass presigned URLs through RedactURL,
// whose signature lives in the query string.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
)

// Attribute keys every record shares.
const (
	JobIDKey  = "jobId"
	UserIDKey = "userId"
)

// New returns a logger writing JSON records to w at level and above.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replace,
	})
	return slog.New(contextHandler{Handler: base})
}

// NewFromEnv returns a logger configured by LOG_LEVEL, defaulting to info when
// the variable is unset or unreadable. It reports the bad value through the
// logger it returns rather than failing: losing logs is worse than logging at
// the wrong level.
func NewFromEnv(w io.Writer) *slog.Logger {
	raw := os.Getenv("LOG_LEVEL")
	level, err := ParseLevel(raw)
	logger := New(w, level)
	if err != nil {
		logger.Warn("unreadable LOG_LEVEL, defaulting to info", slog.String("value", raw))
	}
	return logger
}

// ParseLevel maps a LOG_LEVEL value to a slog level. An empty value is info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("parse log level %q: unknown level", s)
	}
}

// replace renames slog's defaults to the keys AWS Lambda's JSON log format
// uses, and trims the timestamp to milliseconds.
func replace(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.String("timestamp", a.Value.Any().(time.Time).UTC().Format("2006-01-02T15:04:05.000Z"))
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

// contextHandler copies the job and user held by the context onto every
// record, so no call site has to remember them.
//
// The attributes land wherever the handler is currently writing. Call WithGroup
// on a logger from New and they nest inside that group instead of staying at
// the top level, so group a child logger only when that is what you want.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if v := JobID(ctx); v != "" {
		r.AddAttrs(slog.String(JobIDKey, v))
	}
	if v := UserID(ctx); v != "" {
		r.AddAttrs(slog.String(UserIDKey, v))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}

type ctxKey int

const (
	jobIDCtxKey ctxKey = iota
	userIDCtxKey
	loggerCtxKey
)

// WithJob returns a context whose records carry both identifiers.
func WithJob(ctx context.Context, jobID, userID string) context.Context {
	return WithUserID(WithJobID(ctx, jobID), userID)
}

// WithJobID returns a context whose records carry jobID.
func WithJobID(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, jobIDCtxKey, jobID)
}

// WithUserID returns a context whose records carry userID.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey, userID)
}

// JobID returns the job carried by ctx, or an empty string.
func JobID(ctx context.Context) string { return stringValue(ctx, jobIDCtxKey) }

// UserID returns the user carried by ctx, or an empty string.
func UserID(ctx context.Context) string { return stringValue(ctx, userIDCtxKey) }

func stringValue(ctx context.Context, key ctxKey) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(key).(string)
	return v
}

// Into returns a context carrying logger, for the handlers that take only a
// context down into internal packages.
func Into(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey, logger)
}

// FromContext returns the logger stored by Into, or slog.Default.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(loggerCtxKey).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}

// RedactURL strips the query and fragment from raw, which is what makes a
// presigned S3 URL usable by whoever reads the log. The path can still name a
// bucket and key, so log it only where that is acceptable.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[unparseable url]"
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}

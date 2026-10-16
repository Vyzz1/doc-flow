// Package api holds the HTTP API's request handling, so the Lambda
// entrypoints under cmd/ stay thin: parse the event, call in here, return.
//
// Handlers here speak API Gateway's HTTP API (payload format 2.0) types
// directly. That is the one place the AWS event shape leaks in; everything
// below takes plain arguments.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"docflow/internal/logging"
	"docflow/internal/model"
	"docflow/internal/store"
)

// ErrUnauthenticated means the request carried no usable identity.
var ErrUnauthenticated = errors.New("request has no authenticated user")

// Request and Response are the HTTP API's payload format 2.0 shapes.
type (
	Request  = events.APIGatewayV2HTTPRequest
	Response = events.APIGatewayV2HTTPResponse
)

// UserID returns the authenticated subject from the request's JWT
// authorizer. The authorizer has already verified the token; reading "sub"
// here is reading a claim API Gateway vouched for, not trusting the client.
func UserID(r Request) (string, error) {
	jwt := r.RequestContext.Authorizer
	if jwt == nil || jwt.JWT == nil {
		return "", ErrUnauthenticated
	}
	sub := strings.TrimSpace(jwt.JWT.Claims["sub"])
	if sub == "" {
		return "", ErrUnauthenticated
	}
	// The subject becomes part of a DynamoDB key and an S3 prefix, so it has
	// to survive the same check a stored job's owner does.
	if err := model.ValidateUserID(sub); err != nil {
		return "", fmt.Errorf("token subject: %w: %w", err, ErrUnauthenticated)
	}
	return sub, nil
}

// JSON renders body as a JSON response.
func JSON(status int, body any) (Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode response: %w", err)
	}
	return Response{
		StatusCode: status,
		Headers:    map[string]string{"content-type": "application/json"},
		Body:       string(encoded),
	}, nil
}

// errorBody is the one error shape the API returns.
type errorBody struct {
	Error string `json:"error"`
}

// Fail renders an error response and logs the cause.
//
// The message reaches the client; the error does not. That split is what
// keeps a DynamoDB message or a bucket name out of an API response while
// still leaving it in CloudWatch.
func Fail(ctx context.Context, status int, message string, cause error) (Response, error) {
	logger := logging.FromContext(ctx)
	attrs := []any{slog.Int("status", status)}
	if cause != nil {
		attrs = append(attrs, slog.String("error", cause.Error()))
	}
	if status >= http.StatusInternalServerError {
		logger.ErrorContext(ctx, message, attrs...)
	} else {
		logger.WarnContext(ctx, message, attrs...)
	}
	return JSON(status, errorBody{Error: message})
}

// StatusFor maps a store or model error to the status it deserves. Anything
// unrecognized is a 500: an unexpected error is the server's fault until
// shown otherwise.
func StatusFor(err error) int {
	switch {
	case errors.Is(err, store.ErrJobNotFound), errors.Is(err, store.ErrResultNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrJobExists), errors.Is(err, store.ErrStatusConflict):
		return http.StatusConflict
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, model.ErrUnsupportedContentType),
		errors.Is(err, model.ErrInvalidDocType),
		errors.Is(err, model.ErrInvalidUserID),
		errors.Is(err, model.ErrInvalidJobID):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

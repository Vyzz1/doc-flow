// Package quota enforces the per-user daily page limit on paid OCR.
//
// Tesseract is free and ungated. Textract bills per page, so a run through it
// has to be paid for out of a daily allowance that one user cannot overspend,
// whatever happens concurrently.
//
// The counter is a single DynamoDB item per user per UTC day, incremented
// with a conditional ADD. That is one write, atomic, with no read first: two
// invocations racing at the limit cannot both succeed, which a read followed
// by a write could not promise.
package quota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"docflow/internal/model"
	"docflow/internal/store"
)

// DefaultDailyPages is the allowance when TEXTRACT_DAILY_PAGES is unset.
// Textract bills per page, so this is deliberately small: it is a safety net
// against a loop or a stuck retry, not a product limit.
const DefaultDailyPages = 20

// Errors reported when an allowance cannot be spent.
var (
	// ErrExceeded means the request would pass the user's daily limit.
	// Callers fall back to the free engine rather than failing the job.
	ErrExceeded = errors.New("daily quota exceeded")

	// ErrInvalidPages means a caller asked to spend a nonsensical amount.
	ErrInvalidPages = errors.New("page count must be positive")
)

// pagesAttr is the counter attribute on a quota item.
const pagesAttr = "pages"

// Reservation is what a successful Reserve spent and what remains.
type Reservation struct {
	Day       string
	Pages     int
	UsedToday int
	Limit     int
}

// Remaining reports how much of the allowance is left after this
// reservation.
func (r Reservation) Remaining() int {
	if r.Limit < r.UsedToday {
		return 0
	}
	return r.Limit - r.UsedToday
}

// Quota decides whether a user may spend paid OCR pages.
type Quota interface {
	// Reserve adds pages to the user's counter for the day containing now,
	// or fails with ErrExceeded without spending anything.
	Reserve(ctx context.Context, userID string, pages int, now time.Time) (Reservation, error)
}

// API is the slice of the DynamoDB client this package uses.
type API interface {
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// DynamoQuota is the DynamoDB-backed Quota.
type DynamoQuota struct {
	client API
	table  string
	limit  int
}

// New returns a quota enforcing limit pages per user per day.
func New(client API, table string, limit int) *DynamoQuota {
	if limit < 0 {
		limit = 0
	}
	return &DynamoQuota{client: client, table: table, limit: limit}
}

// NewFromEnv builds a quota from TABLE_NAME and TEXTRACT_DAILY_PAGES.
//
// An unreadable limit falls back to the default rather than failing: the
// wrong allowance is recoverable, a Lambda that will not start is not.
func NewFromEnv(ctx context.Context) (*DynamoQuota, error) {
	table := strings.TrimSpace(os.Getenv("TABLE_NAME"))
	if table == "" {
		return nil, errors.New("TABLE_NAME is not set")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint := strings.TrimSpace(os.Getenv("DYNAMODB_ENDPOINT")); endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return New(client, table, DailyPagesFromEnv()), nil
}

// DailyPagesFromEnv reads TEXTRACT_DAILY_PAGES, falling back to the default.
func DailyPagesFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("TEXTRACT_DAILY_PAGES"))
	if raw == "" {
		return DefaultDailyPages
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return DefaultDailyPages
	}
	return limit
}

var _ Quota = (*DynamoQuota)(nil)

// Reserve spends pages of the user's allowance for the UTC day containing
// now.
//
// The condition compares the stored counter against the allowance minus this
// request, so the check and the increment are one operation. A limit of zero
// refuses everything without touching the table.
func (q *DynamoQuota) Reserve(ctx context.Context, userID string, pages int, now time.Time) (Reservation, error) {
	if pages <= 0 {
		return Reservation{}, fmt.Errorf("reserve %d pages: %w", pages, ErrInvalidPages)
	}
	if err := model.ValidateUserID(userID); err != nil {
		return Reservation{}, fmt.Errorf("reserve quota: %w", err)
	}

	day := store.QuotaSK(now)
	if pages > q.limit {
		// Nothing could ever satisfy this, so do not spend a write finding
		// out.
		return Reservation{}, fmt.Errorf("reserve %d pages of a %d page allowance: %w", pages, q.limit, ErrExceeded)
	}

	// The counter is absent on the day's first call, which attribute_not_exists
	// covers; afterwards it has to leave room for this request.
	out, err := q.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(q.table),
		Key: map[string]types.AttributeValue{
			store.PKAttr: &types.AttributeValueMemberS{Value: store.UserPK(userID)},
			store.SKAttr: &types.AttributeValueMemberS{Value: day},
		},
		UpdateExpression:    aws.String("ADD #pages :pages SET #ttl = if_not_exists(#ttl, :ttl)"),
		ConditionExpression: aws.String("attribute_not_exists(#pages) OR #pages <= :headroom"),
		ExpressionAttributeNames: map[string]string{
			"#pages": pagesAttr,
			"#ttl":   store.TTLAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pages":    &types.AttributeValueMemberN{Value: strconv.Itoa(pages)},
			":headroom": &types.AttributeValueMemberN{Value: strconv.Itoa(q.limit - pages)},
			":ttl":      &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UTC().Add(model.Retention).Unix(), 10)},
		},
		ReturnValues: types.ReturnValueUpdatedNew,
	})
	if err != nil {
		if _, ok := errors.AsType[*types.ConditionalCheckFailedException](err); ok {
			return Reservation{}, fmt.Errorf("reserve %d pages for %s: %w", pages, day, ErrExceeded)
		}
		return Reservation{}, fmt.Errorf("reserve %d pages for %s: %w", pages, day, err)
	}

	used, err := usedFrom(out.Attributes)
	if err != nil {
		return Reservation{}, fmt.Errorf("reserve %d pages for %s: %w", pages, day, err)
	}
	return Reservation{Day: day, Pages: pages, UsedToday: used, Limit: q.limit}, nil
}

// usedFrom reads the counter DynamoDB returned after the increment.
func usedFrom(attributes map[string]types.AttributeValue) (int, error) {
	value, found := attributes[pagesAttr]
	if !found {
		return 0, errors.New("update returned no page count")
	}
	number, ok := value.(*types.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("page count is %T, want a number", value)
	}
	used, err := strconv.Atoi(number.Value)
	if err != nil {
		return 0, fmt.Errorf("parse page count %q: %w", number.Value, err)
	}
	return used, nil
}

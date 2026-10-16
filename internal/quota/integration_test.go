//go:build integration

// These run against DynamoDB Local, started by `make up`.
//
// The daily allowance exists to stop one user spending the project's
// Textract budget, and it is enforced by a single conditional ADD rather
// than a read followed by a write. Whether that actually holds under
// concurrency is a property of the database, not of this code, so it is the
// one thing a fake can never establish.
package quota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"docflow/internal/store"
)

func newLiveQuota(t *testing.T, limit int) *DynamoQuota {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("DYNAMODB_ENDPOINT"))
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT is not set; run make up")
	}

	ctx := t.Context()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("ap-southeast-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
		// DynamoDB Local does not return the checksums the SDK validates by
		// default; real DynamoDB does.
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	table := fmt.Sprintf("quota-%d", time.Now().UnixNano())
	if _, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String(store.PKAttr), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String(store.SKAttr), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String(store.PKAttr), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String(store.SKAttr), KeyType: types.KeyTypeRange},
		},
	}); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		//nolint:errcheck // the container is thrown away after the run
		client.DeleteTable(context.WithoutCancel(ctx), &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	})

	return New(client, table, limit)
}

func TestLiveReserveAccumulates(t *testing.T) {
	q := newLiveQuota(t, 10)
	ctx := t.Context()
	now := time.Now()

	for want := 1; want <= 3; want++ {
		got, err := q.Reserve(ctx, "user-1", 1, now)
		if err != nil {
			t.Fatalf("reservation %d: %v", want, err)
		}
		if got.UsedToday != want {
			t.Errorf("UsedToday = %d after %d reservations, want %d", got.UsedToday, want, want)
		}
		if got.Remaining() != 10-want {
			t.Errorf("Remaining() = %d, want %d", got.Remaining(), 10-want)
		}
	}
}

func TestLiveReserveStopsAtTheLimit(t *testing.T) {
	q := newLiveQuota(t, 3)
	ctx := t.Context()
	now := time.Now()

	for i := range 3 {
		if _, err := q.Reserve(ctx, "user-1", 1, now); err != nil {
			t.Fatalf("reservation %d: %v", i+1, err)
		}
	}
	if _, err := q.Reserve(ctx, "user-1", 1, now); !errors.Is(err, ErrExceeded) {
		t.Fatalf("the fourth reservation = %v, want ErrExceeded", err)
	}
}

func TestLiveReserveRefusesAPartialOverrun(t *testing.T) {
	// Four pages against an allowance of five with two already spent must
	// fail outright rather than spending three and reporting success.
	q := newLiveQuota(t, 5)
	ctx := t.Context()
	now := time.Now()

	if _, err := q.Reserve(ctx, "user-1", 2, now); err != nil {
		t.Fatalf("first reservation: %v", err)
	}
	if _, err := q.Reserve(ctx, "user-1", 4, now); !errors.Is(err, ErrExceeded) {
		t.Fatalf("overrunning reservation = %v, want ErrExceeded", err)
	}
	// And the refused request spent nothing, so three pages remain.
	got, err := q.Reserve(ctx, "user-1", 3, now)
	if err != nil {
		t.Fatalf("the remaining three pages were refused: %v", err)
	}
	if got.UsedToday != 5 {
		t.Errorf("UsedToday = %d, want 5", got.UsedToday)
	}
}

// The reason the counter is a conditional ADD and not a read then a write.
// Twenty callers racing for ten pages must spend ten, not more.
func TestLiveConcurrentReservationsCannotOverspend(t *testing.T) {
	const (
		limit   = 10
		callers = 20
	)
	q := newLiveQuota(t, limit)
	ctx := t.Context()
	now := time.Now()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
		refused int
		other   []error
		highest int
	)
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := q.Reserve(ctx, "user-1", 1, now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				granted++
				if got.UsedToday > highest {
					highest = got.UsedToday
				}
			case errors.Is(err, ErrExceeded):
				refused++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if granted != limit {
		t.Errorf("%d callers were granted a page, want exactly %d", granted, limit)
	}
	if refused != callers-limit {
		t.Errorf("%d callers were refused, want %d", refused, callers-limit)
	}
	// The counter never passed the allowance, even momentarily.
	if highest > limit {
		t.Errorf("the counter reached %d, which is past the allowance of %d", highest, limit)
	}
}

func TestLiveSeparateUsersHaveSeparateAllowances(t *testing.T) {
	q := newLiveQuota(t, 1)
	ctx := t.Context()
	now := time.Now()

	for _, user := range []string{"user-1", "user-2"} {
		if _, err := q.Reserve(ctx, user, 1, now); err != nil {
			t.Errorf("%s was refused its first page: %v", user, err)
		}
	}
	if _, err := q.Reserve(ctx, "user-1", 1, now); !errors.Is(err, ErrExceeded) {
		t.Errorf("user-1 got a second page: %v", err)
	}
}

func TestLiveAllowanceResetsWithTheUTCDay(t *testing.T) {
	q := newLiveQuota(t, 1)
	ctx := t.Context()
	today := time.Date(2026, 10, 16, 23, 0, 0, 0, time.UTC)

	if _, err := q.Reserve(ctx, "user-1", 1, today); err != nil {
		t.Fatalf("first day: %v", err)
	}
	if _, err := q.Reserve(ctx, "user-1", 1, today.Add(30*time.Minute)); !errors.Is(err, ErrExceeded) {
		t.Fatalf("the same day was allowed twice")
	}
	// Past midnight UTC the counter is a different item.
	got, err := q.Reserve(ctx, "user-1", 1, today.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("the next day was refused: %v", err)
	}
	if got.UsedToday != 1 {
		t.Errorf("UsedToday = %d on a fresh day, want 1", got.UsedToday)
	}
}

// An abandoned user must leave nothing behind, which means the counter
// carries the attribute the table's TTL is configured on.
func TestLiveCounterExpires(t *testing.T) {
	q := newLiveQuota(t, 5)
	ctx := t.Context()
	now := time.Now()

	if _, err := q.Reserve(ctx, "user-1", 1, now); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	client, ok := q.client.(*dynamodb.Client)
	if !ok {
		t.Fatal("quota is not backed by a real client")
	}
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(q.table),
		Key: map[string]types.AttributeValue{
			store.PKAttr: &types.AttributeValueMemberS{Value: store.UserPK("user-1")},
			store.SKAttr: &types.AttributeValueMemberS{Value: store.QuotaSK(now)},
		},
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if len(out.Item) == 0 {
		t.Fatal("the counter was not stored")
	}
	if _, found := out.Item[store.TTLAttr]; !found {
		t.Errorf("the counter has no %s, so it will never expire", store.TTLAttr)
	}
}

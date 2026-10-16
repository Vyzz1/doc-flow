package quota

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"docflow/internal/store"
)

const (
	testTable = "docflow-test"
	testUser  = "user-1"
)

var testNow = time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

// fakeAPI records the update and replays a counter.
type fakeAPI struct {
	in *dynamodb.UpdateItemInput

	used int
	err  error
}

func (f *fakeAPI) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]types.AttributeValue{
			pagesAttr: &types.AttributeValueMemberN{Value: strconv.Itoa(f.used)},
		},
	}, nil
}

func conditionFailed() error {
	message := "The conditional request failed"
	return &types.ConditionalCheckFailedException{Message: &message}
}

func numberValue(t *testing.T, in *dynamodb.UpdateItemInput, key string) string {
	t.Helper()
	value, ok := in.ExpressionAttributeValues[key].(*types.AttributeValueMemberN)
	if !ok {
		t.Fatalf("%s = %#v, want a number", key, in.ExpressionAttributeValues[key])
	}
	return value.Value
}

func TestReserve(t *testing.T) {
	f := &fakeAPI{used: 3}

	got, err := New(f, testTable, 20).Reserve(context.Background(), testUser, 3, testNow)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if got.UsedToday != 3 || got.Limit != 20 || got.Pages != 3 {
		t.Errorf("Reservation = %+v, want 3 of 20 used", got)
	}
	if got.Remaining() != 17 {
		t.Errorf("Remaining() = %d, want 17", got.Remaining())
	}
	if got.Day != "QUOTA#2026-10-16" {
		t.Errorf("Day = %q, want QUOTA#2026-10-16", got.Day)
	}

	// One write, conditional, with no read first: that is what makes two
	// racing invocations unable to both pass the limit.
	if want := "ADD #pages :pages SET #ttl = if_not_exists(#ttl, :ttl)"; *f.in.UpdateExpression != want {
		t.Errorf("UpdateExpression = %q, want %q", *f.in.UpdateExpression, want)
	}
	if want := "attribute_not_exists(#pages) OR #pages <= :headroom"; *f.in.ConditionExpression != want {
		t.Errorf("ConditionExpression = %q, want %q", *f.in.ConditionExpression, want)
	}
	// The headroom is the limit minus this request, so the stored counter
	// plus this request can never exceed the limit.
	if got := numberValue(t, f.in, ":headroom"); got != "17" {
		t.Errorf(":headroom = %s, want 17", got)
	}
	if got := numberValue(t, f.in, ":pages"); got != "3" {
		t.Errorf(":pages = %s, want 3", got)
	}
}

func TestReserveWritesTheSharedKeyLayout(t *testing.T) {
	f := &fakeAPI{used: 1}

	if _, err := New(f, testTable, 5).Reserve(context.Background(), testUser, 1, testNow); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	pk, ok := f.in.Key[store.PKAttr].(*types.AttributeValueMemberS)
	if !ok || pk.Value != store.UserPK(testUser) {
		t.Errorf("partition key = %#v, want %s", f.in.Key[store.PKAttr], store.UserPK(testUser))
	}
	sk, ok := f.in.Key[store.SKAttr].(*types.AttributeValueMemberS)
	if !ok || sk.Value != store.QuotaSK(testNow) {
		t.Errorf("sort key = %#v, want %s", f.in.Key[store.SKAttr], store.QuotaSK(testNow))
	}
	// The counter expires like everything else, so an abandoned user leaves
	// nothing behind.
	if f.in.ExpressionAttributeNames["#ttl"] != store.TTLAttr {
		t.Errorf("#ttl = %q, want %q", f.in.ExpressionAttributeNames["#ttl"], store.TTLAttr)
	}
	if got := numberValue(t, f.in, ":ttl"); got == "" || got == "0" {
		t.Errorf(":ttl = %q, want a future timestamp", got)
	}
}

func TestReserveCountsByUTCDay(t *testing.T) {
	// Two instants on the same UTC day but different local days share one
	// counter; otherwise a user in a late timezone would get two allowances.
	ict := time.FixedZone("ICT", 7*60*60)
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "morning UTC", at: testNow, want: "QUOTA#2026-10-16"},
		{name: "late evening UTC, next day locally", at: time.Date(2026, 10, 17, 1, 0, 0, 0, ict), want: "QUOTA#2026-10-16"},
		{name: "just past midnight UTC", at: time.Date(2026, 10, 17, 0, 1, 0, 0, time.UTC), want: "QUOTA#2026-10-17"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{used: 1}
			got, err := New(f, testTable, 5).Reserve(context.Background(), testUser, 1, tt.at)
			if err != nil {
				t.Fatalf("Reserve() error = %v", err)
			}
			if got.Day != tt.want {
				t.Errorf("Day = %q, want %q", got.Day, tt.want)
			}
		})
	}
}

func TestReserveRefusesWhenExceeded(t *testing.T) {
	f := &fakeAPI{err: conditionFailed()}

	_, err := New(f, testTable, 20).Reserve(context.Background(), testUser, 5, testNow)
	if !errors.Is(err, ErrExceeded) {
		t.Fatalf("Reserve() error = %v, want ErrExceeded", err)
	}
}

func TestReserveRefusesImpossibleRequestsWithoutWriting(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		pages int
	}{
		{name: "more than the whole allowance", limit: 20, pages: 21},
		{name: "a zero allowance refuses everything", limit: 0, pages: 1},
		{name: "a negative limit is clamped to zero", limit: -5, pages: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{}
			_, err := New(f, testTable, tt.limit).Reserve(context.Background(), testUser, tt.pages, testNow)
			if !errors.Is(err, ErrExceeded) {
				t.Fatalf("Reserve() error = %v, want ErrExceeded", err)
			}
			if f.in != nil {
				t.Error("Reserve() spent a write on a request nothing could satisfy")
			}
		})
	}
}

func TestReserveRejectsBadInput(t *testing.T) {
	tests := []struct {
		name   string
		userID string
		pages  int
	}{
		{name: "zero pages", userID: testUser, pages: 0},
		{name: "negative pages", userID: testUser, pages: -1},
		{name: "empty user", userID: "", pages: 1},
		{name: "user with a key separator", userID: "USER#1", pages: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{}
			if _, err := New(f, testTable, 20).Reserve(context.Background(), tt.userID, tt.pages, testNow); err == nil {
				t.Fatal("Reserve() = nil, want an error")
			}
			if f.in != nil {
				t.Error("Reserve() called DynamoDB with invalid input")
			}
		})
	}
}

func TestReserveWrapsTransportErrors(t *testing.T) {
	sentinel := errors.New("throttled")
	f := &fakeAPI{err: sentinel}

	_, err := New(f, testTable, 20).Reserve(context.Background(), testUser, 1, testNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Reserve() error = %v, want it to wrap %v", err, sentinel)
	}
	// A throttled write is not a spent allowance; the caller may retry.
	if errors.Is(err, ErrExceeded) {
		t.Error("a transport failure was reported as ErrExceeded")
	}
}

func TestReserveRejectsAnUnreadableCounter(t *testing.T) {
	tests := []struct {
		name       string
		attributes map[string]types.AttributeValue
	}{
		{name: "missing", attributes: map[string]types.AttributeValue{}},
		{name: "not a number", attributes: map[string]types.AttributeValue{pagesAttr: &types.AttributeValueMemberS{Value: "three"}}},
		{name: "unparseable", attributes: map[string]types.AttributeValue{pagesAttr: &types.AttributeValueMemberN{Value: "three"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := usedFrom(tt.attributes); err == nil {
				t.Error("usedFrom() = nil, want an error")
			}
		})
	}
}

func TestRemainingNeverGoesNegative(t *testing.T) {
	// A limit lowered between two days can leave a counter above it.
	r := Reservation{UsedToday: 25, Limit: 20}
	if got := r.Remaining(); got != 0 {
		t.Errorf("Remaining() = %d, want 0", got)
	}
}

func TestDailyPagesFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{name: "unset", set: false, want: DefaultDailyPages},
		{name: "empty", value: "", set: true, want: DefaultDailyPages},
		{name: "a number", value: "50", set: true, want: 50},
		{name: "padded", value: "  50  ", set: true, want: 50},
		{name: "zero disables the fallback", value: "0", set: true, want: 0},
		{name: "negative", value: "-1", set: true, want: DefaultDailyPages},
		{name: "nonsense", value: "lots", set: true, want: DefaultDailyPages},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("TEXTRACT_DAILY_PAGES", tt.value)
			} else {
				t.Setenv("TEXTRACT_DAILY_PAGES", "")
			}
			if got := DailyPagesFromEnv(); got != tt.want {
				t.Errorf("DailyPagesFromEnv() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNewFromEnvNeedsATableName(t *testing.T) {
	t.Setenv("TABLE_NAME", "  ")
	if _, err := NewFromEnv(context.Background()); err == nil {
		t.Fatal("NewFromEnv() = nil without TABLE_NAME, want an error")
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("TABLE_NAME", "docflow")
	t.Setenv("AWS_REGION", "ap-southeast-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	t.Setenv("TEXTRACT_DAILY_PAGES", "7")

	q, err := NewFromEnv(context.Background())
	if err != nil {
		t.Fatalf("NewFromEnv() error = %v", err)
	}
	if q.limit != 7 {
		t.Errorf("limit = %d, want 7", q.limit)
	}
	if q.table != "docflow" {
		t.Errorf("table = %q, want docflow", q.table)
	}
}

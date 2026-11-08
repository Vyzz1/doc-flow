package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// childKey is the key projection a Query returns for one item of a job's
// partition.
func childKey(sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		pkAttr: &types.AttributeValueMemberS{Value: jobPK(testJobID)},
		skAttr: &types.AttributeValueMemberS{Value: sk},
	}
}

func TestDeleteJob(t *testing.T) {
	f := &fakeAPI{queryOut: &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{
		childKey("STEP#validate"),
		childKey("STEP#recognize"),
		childKey(resultSK),
	}}}

	if err := newTestStore(f).DeleteJob(context.Background(), testUser, testJobID); err != nil {
		t.Fatalf("DeleteJob() error = %v", err)
	}

	// The job's own item first, then each child.
	if len(f.deletes) != 4 {
		t.Fatalf("sent %d deletes, want 4 (the job and three children)", len(f.deletes))
	}
	first := f.deletes[0]
	if got := stringAttr(t, first.Key, pkAttr); got != userPK(testUser) {
		t.Errorf("first delete pk = %q, want %q", got, userPK(testUser))
	}
	if got := stringAttr(t, first.Key, skAttr); got != jobSK(testJobID) {
		t.Errorf("first delete sk = %q, want %q", got, jobSK(testJobID))
	}
	for i, sk := range []string{"STEP#validate", "STEP#recognize", resultSK} {
		in := f.deletes[i+1]
		if got := stringAttr(t, in.Key, pkAttr); got != jobPK(testJobID) {
			t.Errorf("child %d pk = %q, want %q", i, got, jobPK(testJobID))
		}
		if got := stringAttr(t, in.Key, skAttr); got != sk {
			t.Errorf("child %d sk = %q, want %q", i, got, sk)
		}
		if in.ConditionExpression != nil {
			t.Errorf("child %d has a condition; only the job's own delete is guarded", i)
		}
	}
}

func TestDeleteJobIsConditionedOnStatus(t *testing.T) {
	f := &fakeAPI{}

	if err := newTestStore(f).DeleteJob(context.Background(), testUser, testJobID); err != nil {
		t.Fatalf("DeleteJob() error = %v", err)
	}

	in := f.deletes[0]
	if in.ConditionExpression == nil {
		t.Fatal("the job's delete has no condition")
	}
	// PROCESSING must be absent from the values the condition allows.
	allowed := map[string]bool{}
	for _, value := range in.ExpressionAttributeValues {
		allowed[value.(*types.AttributeValueMemberS).Value] = true
	}
	for _, status := range []string{"PENDING", "DONE", "FAILED"} {
		if !allowed[status] {
			t.Errorf("condition does not allow %s", status)
		}
	}
	if allowed["PROCESSING"] {
		t.Error("condition allows PROCESSING, which would delete a job under a running workflow")
	}
	if in.ReturnValuesOnConditionCheckFailure != types.ReturnValuesOnConditionCheckFailureAllOld {
		t.Error("the old item is not requested, so a missing job cannot be told from a busy one")
	}
}

func TestDeleteJobFailedCondition(t *testing.T) {
	tests := []struct {
		name string
		item map[string]types.AttributeValue
		want error
	}{
		{name: "no such job", item: nil, want: ErrJobNotFound},
		{name: "job is processing", item: map[string]types.AttributeValue{pkAttr: &types.AttributeValueMemberS{Value: "x"}}, want: ErrStatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{deleteErrs: map[int]error{0: conditionFailed(tt.item)}}

			err := newTestStore(f).DeleteJob(context.Background(), testUser, testJobID)
			if !errors.Is(err, tt.want) {
				t.Errorf("DeleteJob() error = %v, want %v", err, tt.want)
			}
			// A refused delete must not go on to remove the job's results.
			if f.query != nil || len(f.deletes) != 1 {
				t.Errorf("went on after the refusal: %d deletes, query %v", len(f.deletes), f.query)
			}
		})
	}
}

func TestDeleteJobFollowsPages(t *testing.T) {
	f := &fakeAPI{queryOut: &dynamodb.QueryOutput{
		Items:            []map[string]types.AttributeValue{childKey(resultSK)},
		LastEvaluatedKey: childKey(resultSK),
	}}
	pages := 0
	paged := &pagedAPI{fakeAPI: f, pages: &pages}

	if err := New(paged, testTable).DeleteJob(context.Background(), testUser, testJobID); err != nil {
		t.Fatalf("DeleteJob() error = %v", err)
	}
	if pages != 2 {
		t.Errorf("read %d pages, want 2", pages)
	}
}

// pagedAPI answers the first Query with a continuation key and the second
// without, so the loop has to take both.
type pagedAPI struct {
	*fakeAPI
	pages *int
}

func (p *pagedAPI) Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	*p.pages++
	if *p.pages == 1 {
		return p.fakeAPI.Query(ctx, in, opts...)
	}
	if in.ExclusiveStartKey == nil {
		return nil, errors.New("second page requested without the continuation key")
	}
	return &dynamodb.QueryOutput{}, nil
}

func TestDeleteJobErrors(t *testing.T) {
	sentinel := errors.New("throttled")

	tests := []struct {
		name string
		api  API
	}{
		{name: "the job's delete fails", api: &fakeAPI{deleteErrs: map[int]error{0: sentinel}}},
		// The first delete passes and the listing after it fails.
		{name: "listing the children fails", api: &failingQuery{fakeAPI: &fakeAPI{}, err: sentinel}},
		{name: "a child's delete fails", api: &fakeAPI{
			queryOut:   &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{childKey(resultSK)}},
			deleteErrs: map[int]error{1: sentinel},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := New(tt.api, testTable).DeleteJob(context.Background(), testUser, testJobID); !errors.Is(err, sentinel) {
				t.Errorf("DeleteJob() error = %v, want it to wrap %v", err, sentinel)
			}
		})
	}
}

type failingQuery struct {
	*fakeAPI
	err error
}

func (f *failingQuery) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return nil, f.err
}

package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var _ Remover = (*S3)(nil)

// Delete removes one object. S3 reports success for a key that holds nothing,
// which is what makes repeating a delete safe.
func (s *S3) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("delete object: %w", ErrEmptyKey)
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

// DeletePrefix removes every object under prefix, a page at a time. A listing
// returns at most 1000 keys, which is also the most one delete call takes.
func (s *S3) DeletePrefix(ctx context.Context, prefix string) error {
	if !strings.HasSuffix(prefix, "/") || strings.Trim(prefix, "/") == "" {
		return fmt.Errorf("delete prefix %q: %w", prefix, ErrInvalidPrefix)
	}

	var token *string
	for {
		listed, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            &s.bucket,
			Prefix:            &prefix,
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list %s: %w", prefix, err)
		}

		if len(listed.Contents) > 0 {
			objects := make([]types.ObjectIdentifier, 0, len(listed.Contents))
			for _, object := range listed.Contents {
				objects = append(objects, types.ObjectIdentifier{Key: object.Key})
			}
			out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: &s.bucket,
				// Quiet reports only the failures, which is all this reads.
				Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
			})
			if err != nil {
				return fmt.Errorf("delete under %s: %w", prefix, err)
			}
			// A delete call can succeed as a whole while refusing single keys.
			if len(out.Errors) > 0 {
				first := out.Errors[0]
				return fmt.Errorf("delete under %s: %d keys refused, first %s: %s",
					prefix, len(out.Errors), aws.ToString(first.Key), aws.ToString(first.Code))
			}
		}

		if !aws.ToBool(listed.IsTruncated) {
			return nil
		}
		token = listed.NextContinuationToken
	}
}

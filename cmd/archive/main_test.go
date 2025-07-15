package main

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"go.uber.org/zap"
)

type fakeS3 struct {
	copyErr   error
	copyCalls int
}

func (f *fakeS3) GetObjectTagging(ctx context.Context, in *s3.GetObjectTaggingInput, opt ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error) {
	return &s3.GetObjectTaggingOutput{}, nil
}

func (f *fakeS3) CopyObject(ctx context.Context, in *s3.CopyObjectInput, opt ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	f.copyCalls++
	if f.copyCalls == 1 && f.copyErr != nil {
		return nil, f.copyErr
	}
	return &s3.CopyObjectOutput{}, nil
}

func (f *fakeS3) PutObjectTagging(ctx context.Context, in *s3.PutObjectTaggingInput, opt ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error) {
	return &s3.PutObjectTaggingOutput{}, nil
}

type fakeDB struct {
	err error
}

func (f *fakeDB) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

type fakeCW struct{ called bool }

func (f *fakeCW) PutMetricData(ctx context.Context, in *cloudwatch.PutMetricDataInput, opt ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricDataOutput, error) {
	f.called = true
	return &cloudwatch.PutMetricDataOutput{}, nil
}

func TestHandlerSuccess(t *testing.T) {
	s3Client = &fakeS3{}
	dbClient = &fakeDB{}
	cw := &fakeCW{}
	cwClient = cw
	log = zap.NewNop().Sugar()
	now = func() time.Time { return time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC) }

	evt := ArchiveEvent{RowsProcessed: 5, RowsFailed: 1}
	evt.Records = []events.S3EventRecord{{S3: events.S3Entity{Bucket: events.S3Bucket{Name: "b"}, Object: events.S3Object{Key: "k"}}}}

	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !cw.called {
		t.Fatalf("metric not sent")
	}
}

func TestHandlerSlowdownRetry(t *testing.T) {
	apiErr := &smithy.GenericAPIError{Code: "SlowDown", Message: "slow"}
	fs3 := &fakeS3{copyErr: apiErr}
	s3Client = fs3
	dbClient = &fakeDB{}
	cwClient = &fakeCW{}
	log = zap.NewNop().Sugar()
	now = func() time.Time { return time.Time{} }

	evt := ArchiveEvent{}
	evt.Records = []events.S3EventRecord{{S3: events.S3Entity{Bucket: events.S3Bucket{Name: "b"}, Object: events.S3Object{Key: "k"}}}}

	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if fs3.copyCalls != 2 {
		t.Fatalf("expected 2 copy calls, got %d", fs3.copyCalls)
	}
}

func TestHandlerConditionalFailure(t *testing.T) {
	s3Client = &fakeS3{}
	dbClient = &fakeDB{err: &dbtypes.ConditionalCheckFailedException{}}
	cwClient = &fakeCW{}
	log = zap.NewNop().Sugar()
	now = func() time.Time { return time.Time{} }

	evt := ArchiveEvent{}
	evt.Records = []events.S3EventRecord{{S3: events.S3Entity{Bucket: events.S3Bucket{Name: "b"}, Object: events.S3Object{Key: "k"}}}}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
}

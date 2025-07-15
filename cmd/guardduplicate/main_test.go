package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/your-org/file-processor-sample/internal/guard"
	"go.uber.org/zap"
)

type stubBody struct {
	io.Reader
	closed bool
	err    error
}

func (s *stubBody) Close() error { s.closed = true; return s.err }

// --- s3 stub ---
type stubS3 struct {
	out   *stubBody
	err   error
	input *string
}

func (s *stubS3) GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	s.input = in.Key
	if s.err != nil {
		return nil, s.err
	}
	return &s3.GetObjectOutput{Body: s.out}, nil
}

// --- dynamo stub ---
type stubDDB struct {
	putErr error
	item   map[string]types.AttributeValue
}

func (d *stubDDB) PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	d.item = in.Item
	if d.putErr != nil {
		return nil, d.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func newEvent(size int64) events.S3Event {
	return events.S3Event{Records: []events.S3EventRecord{{S3: events.S3Entity{Bucket: events.S3Bucket{Name: "b"}, Object: events.S3Object{Key: "k", Size: size}}}}}
}

func setup(s3c s3API, db ddbAPI) {
	tableName = "tbl"
	s3Client = s3c
	dbClient = db
	log = zap.NewNop().Sugar()
}

func TestHandlerTooLarge(t *testing.T) {
	setup(nil, nil)
	evt := newEvent(guard.MaxSize + 1)
	if err := handler(context.Background(), evt); err == nil {
		t.Fatalf("expected size error")
	}
}

func TestHandlerGetObjectError(t *testing.T) {
	s3c := &stubS3{err: errors.New("boom")}
	setup(s3c, nil)
	evt := newEvent(1)
	if err := handler(context.Background(), evt); err == nil || err.Error() != "get object: boom" {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestHandlerReadError(t *testing.T) {
	body := &stubBody{Reader: &errorReader{}}
	s3c := &stubS3{out: body}
	setup(s3c, nil)
	evt := newEvent(1)
	if err := handler(context.Background(), evt); err == nil || err.Error() != "read object: read err" {
		t.Fatalf("unexpected err: %v", err)
	}
	if !body.closed {
		t.Fatalf("body not closed")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read err") }

func TestHandlerPutError(t *testing.T) {
	body := &stubBody{Reader: bytes.NewBufferString("ok")}
	s3c := &stubS3{out: body}
	db := &stubDDB{putErr: errors.New("bad")}
	setup(s3c, db)
	evt := newEvent(1)
	if err := handler(context.Background(), evt); err == nil || err.Error() != "write manifest: bad" {
		t.Fatalf("unexpected err: %v", err)
	}
	if !body.closed {
		t.Fatalf("body not closed")
	}
}

func TestHandlerSuccess(t *testing.T) {
	body := &stubBody{Reader: bytes.NewBufferString("data"), err: errors.New("c")}
	s3c := &stubS3{out: body}
	db := &stubDDB{}
	setup(s3c, db)
	evt := newEvent(1)
	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !body.closed {
		t.Fatalf("body not closed")
	}
	if _, ok := db.item["SHA256"]; !ok {
		t.Fatalf("ddb not called")
	}
}

func TestMainFunc(t *testing.T) {
	os.Setenv("AWS_REGION", "us-east-1")
	os.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	called := false
	start = func(i interface{}) { called = true }
	loadConfig = func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, nil
	}
	main()
	if !called {
		t.Fatalf("start not called")
	}
}

func TestMainFuncError(t *testing.T) {
	start = func(i interface{}) {}
	loadConfig = func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("cfg")
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	main()
}

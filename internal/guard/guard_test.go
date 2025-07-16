package guard

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"
)

func TestValidateSize(t *testing.T) {
	err := ValidateSize("foo.txt", MaxSize-1)
	if err != nil {
		t.Errorf("unexpected error for valid size: %v", err)
	}
	err = ValidateSize("foo.txt", MaxSize+1)
	if err == nil {
		t.Error("expected error for oversized file, got nil")
	}
}

func TestComputeSHA256(t *testing.T) {
	data := []byte("hello world")
	hash, err := ComputeSHA256(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" {
		t.Errorf("unexpected hash: %s", hash)
	}
}

// errorReader always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) { return 0, errors.New("fail") }

func TestComputeSHA256_Error(t *testing.T) {
	_, err := ComputeSHA256(&errorReader{})
	if err == nil {
		t.Error("expected error from ComputeSHA256, got nil")
	}
}

type mockDynamo struct {
	putErr error
	input  *dynamodb.PutItemInput
}

func (m *mockDynamo) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.input = params
	return &dynamodb.PutItemOutput{}, m.putErr
}

func TestPutManifest(t *testing.T) {
	m := &mockDynamo{}
	err := PutManifest(context.Background(), m, "tbl", "key", "sum")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if m.input == nil || m.input.TableName == nil || *m.input.TableName != "tbl" {
		t.Error("table name not set correctly")
	}
	if m.input.Item["FileKey"].(*types.AttributeValueMemberS).Value != "key" {
		t.Error("FileKey not set correctly")
	}
	if m.input.Item["SHA256"].(*types.AttributeValueMemberS).Value != "sum" {
		t.Error("SHA256 not set correctly")
	}
	if m.input.Item["Processed"].(*types.AttributeValueMemberBOOL).Value != false {
		t.Error("Processed not set correctly")
	}

	m.putErr = errors.New("fail")
	err = PutManifest(context.Background(), m, "tbl", "key", "sum")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// zap.SugaredLogger is hard to test, so just ensure Close calls Close and logs error

type errCloser struct{ closed bool }

func (e *errCloser) Close() error {
	e.closed = true
	return errors.New("fail")
}

func TestClose(t *testing.T) {
	logger := zap.NewExample().Sugar()
	c := &errCloser{}
	Close(c, logger)
	if !c.closed {
		t.Error("Close not called")
	}
}

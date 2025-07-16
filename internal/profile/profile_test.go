package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"go.uber.org/zap"
)

type mockSSM struct {
	value string
	err   error
	calls int
}

func (m *mockSSM) GetParameter(ctx context.Context, in *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &types.Parameter{Value: &m.value},
	}, nil
}

func TestLoader_Load_SuccessAndCache(t *testing.T) {
	val := `{"foo": 123}`
	m := &mockSSM{value: val}
	logger := zap.NewExample().Sugar()
	l := New(m, logger)

	// First call: should hit SSM
	res, err := l.Load(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res["foo"] != float64(123) {
		t.Errorf("unexpected value: %v", res["foo"])
	}
	if m.calls != 1 {
		t.Errorf("expected 1 call, got %d", m.calls)
	}

	// Second call: should use cache
	_, err = l.Load(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.calls != 1 {
		t.Errorf("expected 1 call after cache, got %d", m.calls)
	}
}

func TestLoader_Load_Error(t *testing.T) {
	m := &mockSSM{err: errors.New("fail")}
	logger := zap.NewExample().Sugar()
	l := New(m, logger)
	_, err := l.Load(context.Background(), "bad")
	if err == nil || err.Error() != "get parameter bad: fail" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoader_Load_BadJSON(t *testing.T) {
	m := &mockSSM{value: "notjson"}
	logger := zap.NewExample().Sugar()
	l := New(m, logger)
	_, err := l.Load(context.Background(), "badjson")
	if err == nil || err.Error()[:14] != "decode profile" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ...existing code...

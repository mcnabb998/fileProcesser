package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"go.uber.org/zap"
	"strings"
)

type fakeStore struct {
	mu         sync.Mutex
	token      string
	exp        time.Time
	refreshing bool
	fail       bool
	lockFail   bool
	lockErr    bool
	saveErr    bool
}

func (f *fakeStore) Get(ctx context.Context) (string, time.Time, bool, error) {
	if f.fail {
		return "", time.Time{}, false, fmt.Errorf("dynamo down")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token, f.exp, f.refreshing, nil
}

func (f *fakeStore) TryLock(ctx context.Context) (bool, error) {
	if f.fail {
		return false, fmt.Errorf("dynamo down")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lockErr {
		return false, fmt.Errorf("lock err")
	}
	if f.refreshing || f.lockFail {
		return false, nil
	}
	f.refreshing = true
	return true, nil
}

func (f *fakeStore) Save(ctx context.Context, token string, exp time.Time) error {
	if f.fail {
		return fmt.Errorf("dynamo down")
	}
	if f.saveErr {
		return fmt.Errorf("save error")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = token
	f.exp = exp
	f.refreshing = false
	return nil
}

func (f *fakeStore) Unlock(ctx context.Context) error {
	if f.fail {
		return fmt.Errorf("dynamo down")
	}
	f.mu.Lock()
	f.refreshing = false
	f.mu.Unlock()
	return nil
}

type fakeCW struct {
	mu  sync.Mutex
	cnt int
}

func (f *fakeCW) PutMetricData(ctx context.Context, in *cloudwatch.PutMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricDataOutput, error) {
	f.mu.Lock()
	f.cnt += len(in.MetricData)
	f.mu.Unlock()
	return &cloudwatch.PutMetricDataOutput{}, nil
}

func TestCachedToken(t *testing.T) {
	store := &fakeStore{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"access_token":"t1"}`)
	}))
	defer srv.Close()
	b := &Broker{
		store:      store,
		cw:         &fakeCW{},
		httpClient: srv.Client(),
		sfURL:      srv.URL,
		creds:      map[string]string{"grant_type": "password"},
		log:        zap.NewNop().Sugar(),
	}
	resp, err := b.handler(context.Background(), events.APIGatewayV2HTTPRequest{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	resp, err = b.handler(context.Background(), events.APIGatewayV2HTTPRequest{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("second call error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("token was not cached")
	}
}

func TestConcurrentLock(t *testing.T) {
	store := &fakeStore{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"access_token":"t2"}`)
	}))
	defer srv.Close()
	b := &Broker{store: store, cw: &fakeCW{}, httpClient: srv.Client(), sfURL: srv.URL, creds: map[string]string{"grant_type": "password"}, log: zap.NewNop().Sugar()}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { b.getToken(context.Background()); wg.Done() }()
	go func() { b.getToken(context.Background()); wg.Done() }()
	wg.Wait()
	if calls != 1 {
		t.Fatalf("expected 1 refresh, got %d", calls)
	}
}

func TestDynamoUnavailableGrace(t *testing.T) {
	store := &fakeStore{fail: true}
	b := &Broker{store: store, cw: &fakeCW{}, httpClient: http.DefaultClient, sfURL: "", creds: map[string]string{}, log: zap.NewNop().Sugar()}
	b.token = "cached"
	b.expiry = time.Now().Add(-6 * time.Minute)
	tok, err := b.getToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "cached" {
		t.Fatalf("expected cached token, got %s", tok)
	}
}

func TestSalesforce401Rotation(t *testing.T) {
	store := &fakeStore{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"access_token":"new"}`)
	}))
	defer srv.Close()
	b := &Broker{store: store, cw: &fakeCW{}, httpClient: srv.Client(), sfURL: srv.URL, creds: map[string]string{"grant_type": "password"}, log: zap.NewNop().Sugar()}
	tok, err := b.getToken(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tok != "new" || calls != 2 || b.creds["rotate"] != "true" {
		t.Fatalf("rotation not triggered")
	}
}

func TestLockTimeout(t *testing.T) {
	store := &fakeStore{lockFail: true}
	b := &Broker{store: store, cw: &fakeCW{}, httpClient: http.DefaultClient, sfURL: "", creds: map[string]string{"grant_type": "password"}, log: zap.NewNop().Sugar()}
	_, err := b.getToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout")
	}
}

func TestFetchTokenError(t *testing.T) {
	store := &fakeStore{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(401)
	}))
	defer srv.Close()
	b := &Broker{store: store, cw: &fakeCW{}, httpClient: srv.Client(), sfURL: srv.URL, creds: map[string]string{"grant_type": "password"}, log: zap.NewNop().Sugar()}
	_, err := b.getToken(context.Background())
	if err == nil || calls != 2 {
		t.Fatalf("expected error and two calls")
	}
}

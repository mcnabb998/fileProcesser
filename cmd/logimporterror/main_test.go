package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

func loadEvent(t *testing.T) ErrorEvent {
	b, err := os.ReadFile("testdata/event.json")
	if err != nil {
		t.Fatal(err)
	}
	var e ErrorEvent
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestFirstInsert(t *testing.T) {
	evt := loadEvent(t)
	brokerCalls := 0
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		brokerCalls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	sfCalls := 0
	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sfCalls++
		if r.Method != http.MethodPatch {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("bad token")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if sfCalls != 1 || brokerCalls != 1 {
		t.Fatalf("calls sf=%d broker=%d", sfCalls, brokerCalls)
	}
}

func TestDuplicateIncrement(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	patchCount := 0
	var secondPatchBody []byte
	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			patchCount++
			b, _ := io.ReadAll(r.Body)
			if patchCount == 2 {
				secondPatchBody = b
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			io.WriteString(w, `{"Retry_Count__c":1}`)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if patchCount != 2 {
		t.Fatalf("expected 2 patches, got %d", patchCount)
	}
	if !bytes.Contains(secondPatchBody, []byte("\"Retry_Count__c\":2")) {
		t.Fatalf("retry count not incremented: %s", string(secondPatchBody))
	}
}

func TestValidationError(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	patchCount := 0
	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		patchCount++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
	if patchCount != 3 {
		t.Fatalf("expected 3 attempts, got %d", patchCount)
	}
}

func TestBroker401Retry(t *testing.T) {
	evt := loadEvent(t)
	call := 0
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if call != 2 {
		t.Fatalf("expected 2 broker calls, got %d", call)
	}
}

func TestSF503RetryFail(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	attempts := 0
	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestBrokerDouble401(t *testing.T) {
	count := 0
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer broker.Close()

	brokerURL = broker.URL
	log = zap.NewNop().Sugar()
	if _, err := getToken(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if count != 2 {
		t.Fatalf("expected 2 broker calls, got %d", count)
	}
}

func TestBroker500(t *testing.T) {
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broker.Close()

	brokerURL = broker.URL
	log = zap.NewNop().Sugar()
	if _, err := getToken(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSalesforce401Refresh(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	call := 0
	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if call != 2 {
		t.Fatalf("expected 2 calls, got %d", call)
	}
}

func TestRetryGetFailure(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
}

func TestRetryDecodeError(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	sf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNoContent)
		} else {
			io.WriteString(w, "bad json")
		}
	}))
	defer sf.Close()

	brokerURL = broker.URL
	sfAPI = sf.URL
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
}

func TestRetryGetRequestError(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	prevClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return nil, errors.New("boom")
		}
		rr := httptest.NewRecorder()
		rr.Code = http.StatusNoContent
		return rr.Result(), nil
	})}
	brokerURL = broker.URL
	sfAPI = "http://example.com"
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
	httpClient = prevClient
}

func TestRetryUpdateError(t *testing.T) {
	evt := loadEvent(t)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()

	step := 0
	prevClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch && step == 1 {
			return nil, errors.New("boom")
		}
		rr := httptest.NewRecorder()
		if req.Method == http.MethodPatch {
			step++
			rr.Code = http.StatusNoContent
		} else {
			rr.Code = http.StatusOK
			rr.Body.WriteString(`{"Retry_Count__c":1}`)
		}
		return rr.Result(), nil
	})}
	brokerURL = broker.URL
	sfAPI = "http://example.com"
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}

	if err := handler(context.Background(), evt); err == nil {
		t.Fatal("expected error")
	}
	httpClient = prevClient
}

func TestSFRequestErrors(t *testing.T) {
	prevClient := httpClient
	sfAPI = ":bad"
	if _, err := sfRequest(context.Background(), http.MethodGet, "/x", "t", nil); err == nil {
		t.Fatal("expected error")
	}
	httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("boom") })}
	sfAPI = "http://example.com"
	if _, err := sfRequest(context.Background(), http.MethodGet, "/x", "t", nil); err == nil {
		t.Fatal("expected error")
	}
	httpClient = prevClient
}

func TestHandlerTokenError(t *testing.T) {
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broker.Close()
	brokerURL = broker.URL
	sfAPI = "http://example.com"
	log = zap.NewNop().Sugar()
	if err := handler(context.Background(), loadEvent(t)); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandlerRequestError(t *testing.T) {
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok"}`)
	}))
	defer broker.Close()
	brokerURL = broker.URL
	sfAPI = "http://example.com"
	prevClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("boom") })}
	log = zap.NewNop().Sugar()
	sleep = func(time.Duration) {}
	if err := handler(context.Background(), loadEvent(t)); err == nil {
		t.Fatal("expected error")
	}
	httpClient = prevClient
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGetTokenErrors(t *testing.T) {
	// New request error
	brokerURL = ":bad"
	log = zap.NewNop().Sugar()
	if _, err := getToken(context.Background()); err == nil {
		t.Fatal("expected error")
	}

	// HTTP client error
	brokerURL = "http://example.com"
	prev := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})}
	if _, err := getToken(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	httpClient = prev
}

func TestGetTokenDecodeError(t *testing.T) {
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "bad")
	}))
	defer broker.Close()
	brokerURL = broker.URL
	log = zap.NewNop().Sugar()
	if _, err := getToken(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRealMain(t *testing.T) {
	called := false
	start := func(h interface{}) {
		if _, ok := h.(func(context.Context, ErrorEvent) error); ok {
			called = true
		}
	}
	prev := lambdaStart
	lambdaStart = start
	main()
	lambdaStart = prev
	if !called {
		t.Fatal("start not called")
	}
}

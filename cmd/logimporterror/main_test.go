package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dnaeon/go-vcr/recorder"
	"go.uber.org/zap"
)

func TestHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	rec, err := recorder.NewAsMode("testdata/log_error", recorder.ModeRecording, nil)
	log = zap.NewNop().Sugar()
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Stop()
	http.DefaultClient.Transport = rec
	endpoint = srv.URL
	evt := ErrorEvent{FileKey: "f", Error: "bad"}
	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/zap"
)

var (
	endpoint = os.Getenv("BROKER_ENDPOINT")
	log      *zap.SugaredLogger
)

type ErrorEvent struct {
	FileKey string `json:"fileKey"`
	Error   string `json:"error"`
}

func handler(ctx context.Context, evt ErrorEvent) error {
	b, _ := json.Marshal(evt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	log.Infow("error logged", "file", evt.FileKey)
	return nil
}

func main() {
	logger, _ := zap.NewProduction()
	log = logger.Sugar()
	lambda.Start(handler)
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/zap"
)

var (
	brokerURL   = os.Getenv("BROKER_URL")
	sfAPI       = os.Getenv("SF_API")
	log         *zap.SugaredLogger
	sleep       = time.Sleep
	lambdaStart = lambda.Start
	httpClient  = http.DefaultClient
)

// ErrorEvent is triggered when a row import fails.
type ErrorEvent struct {
	ExternalRowID string `json:"externalRowId"`
	Message       string `json:"message"`
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
}

func getToken(ctx context.Context) (string, error) {
	for i := 0; ; i++ {
		if i >= 2 {
			return "", fmt.Errorf("broker status: %s", http.StatusText(http.StatusUnauthorized))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, brokerURL, nil)
		if err != nil {
			return "", fmt.Errorf("new token request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("do token request: %w", err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var tr tokenResp
			if err := json.Unmarshal(b, &tr); err != nil {
				return "", fmt.Errorf("decode token: %w", err)
			}
			return tr.AccessToken, nil
		}
		if resp.StatusCode == http.StatusUnauthorized {
			continue
		}
		return "", fmt.Errorf("broker status: %s", resp.Status)
	}
}

func sfRequest(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, sfAPI+path, r)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return httpClient.Do(req)
}

func handler(ctx context.Context, evt ErrorEvent) error {
	token, err := getToken(ctx)
	if err != nil {
		return err
	}

	path := "/sobjects/Import_Error__c/External_Row_Id__c/" + url.PathEscape(evt.ExternalRowID)
	body := map[string]any{
		"External_Row_Id__c": evt.ExternalRowID,
		"Error_Message__c":   evt.Message,
	}

	var resp *http.Response
	for attempt := 0; attempt <= 2; attempt++ {
		resp, err = sfRequest(ctx, http.MethodPatch, path, token, body)
		if err != nil {
			return fmt.Errorf("patch: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			_ = resp.Body.Close()
			token, err = getToken(ctx)
			if err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode >= 400 {
			_ = resp.Body.Close()
			if attempt == 2 {
				return fmt.Errorf("salesforce status: %s", resp.Status)
			}
			sleep(time.Duration(1<<attempt) * 100 * time.Millisecond)
			continue
		}
		break
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		// fetch current retry count
		gresp, err := sfRequest(ctx, http.MethodGet, path+"?fields=Retry_Count__c", token, nil)
		if err != nil {
			return fmt.Errorf("get retry count: %w", err)
		}
		b, _ := io.ReadAll(gresp.Body)
		_ = gresp.Body.Close()
		if gresp.StatusCode >= 300 {
			return fmt.Errorf("get retry status: %s", gresp.Status)
		}
		var data struct {
			Retry int `json:"Retry_Count__c"`
		}
		if err := json.Unmarshal(b, &data); err != nil {
			return fmt.Errorf("decode retry: %w", err)
		}
		_, err = sfRequest(ctx, http.MethodPatch, path, token, map[string]any{"Retry_Count__c": data.Retry + 1})
		if err != nil {
			return fmt.Errorf("update retry: %w", err)
		}
	}

	log.Infow("error logged", "row", evt.ExternalRowID)
	return nil
}

func realMain(start func(interface{})) {
	logger, _ := zap.NewProduction()
	log = logger.Sugar()
	start(handler)
}

func main() {
	realMain(lambdaStart)
}

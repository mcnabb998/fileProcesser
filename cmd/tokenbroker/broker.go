package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"go.uber.org/zap"
)

type tokenStore interface {
	Get(ctx context.Context) (token string, exp time.Time, refreshing bool, err error)
	TryLock(ctx context.Context) (bool, error)
	Save(ctx context.Context, token string, exp time.Time) error
	Unlock(ctx context.Context) error
}

// Broker implements token retrieval logic.
type metricsClient interface {
	PutMetricData(ctx context.Context, in *cloudwatch.PutMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricDataOutput, error)
}

type Broker struct {
	store      tokenStore
	cw         metricsClient
	httpClient *http.Client
	sfURL      string
	creds      map[string]string
	log        *zap.SugaredLogger
	mu         sync.Mutex
	token      string
	expiry     time.Time
}

const (
	cacheTTL = 5 * time.Minute
	graceTTL = 15 * time.Minute
)

func (b *Broker) getToken(ctx context.Context) (string, error) {
	b.mu.Lock()
	if b.token != "" && time.Now().Before(b.expiry) {
		tok := b.token
		b.mu.Unlock()
		return tok, nil
	}
	b.mu.Unlock()

	tok, exp, refreshing, err := b.store.Get(ctx)
	if err != nil {
		b.log.Warnw("dynamo get", "error", err)
		b.mu.Lock()
		if b.token != "" && time.Now().Before(b.expiry.Add(graceTTL-cacheTTL)) {
			tok := b.token
			b.mu.Unlock()
			return tok, nil
		}
		b.mu.Unlock()
		return "", err
	}
	if tok != "" && time.Now().Before(exp) && !refreshing {
		b.mu.Lock()
		b.token = tok
		b.expiry = exp
		b.mu.Unlock()
		return tok, nil
	}

	locked, err := b.store.TryLock(ctx)
	if err != nil {
		return "", err
	}
	if !locked {
		// Wait for other refresher
		for i := 0; i < 10; i++ {
			time.Sleep(50 * time.Millisecond)
			tok, exp, refreshing, err = b.store.Get(ctx)
			if err == nil && tok != "" && time.Now().Before(exp) && !refreshing {
				b.mu.Lock()
				b.token = tok
				b.expiry = exp
				b.mu.Unlock()
				return tok, nil
			}
		}
		return "", fmt.Errorf("timeout waiting for refresh")
	}

	token, err := b.fetchToken(ctx)
	if err != nil {
		b.store.Unlock(ctx)
		return "", err
	}
	expTime := time.Now().Add(cacheTTL)
	if err := b.store.Save(ctx, token, expTime); err != nil {
		b.log.Warnw("dynamo save", "error", err)
	}
	b.mu.Lock()
	b.token = token
	b.expiry = expTime
	b.mu.Unlock()
	b.cw.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String("TokenBroker"),
		MetricData: []cwtypes.MetricDatum{
			{
				MetricName: aws.String("TokenRefreshCount"),
				Value:      aws.Float64(1),
			},
		},
	})
	return token, nil
}

func (b *Broker) fetchToken(ctx context.Context) (string, error) {
	form := make(urlValues)
	for k, v := range b.creds {
		form[k] = v
	}
	// 2 attempts (handle 401 rotation)
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, b.sfURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		if resp.StatusCode == 401 {
			if rot, ok := b.creds["rotate"]; ok && rot == "true" {
				// already rotated once
				return "", fmt.Errorf("unauthorized")
			}
			b.creds["rotate"] = "true"
			continue
		}
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("salesforce status %s", resp.Status)
		}
		var out struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", err
		}
		resp.Body.Close()
		return out.AccessToken, nil
	}
	return "", fmt.Errorf("unauthorized")
}

type urlValues map[string]string

func (v urlValues) Encode() string {
	var buf strings.Builder
	first := true
	for k, val := range v {
		if k == "rotate" {
			continue
		}
		if !first {
			buf.WriteByte('&')
		}
		first = false
		buf.WriteString(url.QueryEscape(k))
		buf.WriteByte('=')
		buf.WriteString(url.QueryEscape(val))
	}
	return buf.String()
}

func (b *Broker) handler(ctx context.Context, evt events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	start := time.Now()
	tok, err := b.getToken(ctx)
	latency := time.Since(start).Milliseconds()
	_, _ = b.cw.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String("TokenBroker"),
		MetricData: []cwtypes.MetricDatum{
			{
				MetricName: aws.String("BrokerLatencyMs"),
				Value:      aws.Float64(float64(latency)),
			},
		},
	})
	if err != nil {
		b.log.Errorw("get token", "error", err)
		return events.APIGatewayV2HTTPResponse{StatusCode: 500}, nil
	}
	body, _ := json.Marshal(map[string]string{"token": tok})
	return events.APIGatewayV2HTTPResponse{StatusCode: 200, Body: string(body), Headers: map[string]string{"Content-Type": "application/json"}}, nil
}

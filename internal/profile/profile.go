package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"go.uber.org/zap"
)

// Loader retrieves and caches JSON profiles from SSM Parameter Store.
// SSMAPI abstracts the SSM GetParameter operation for testability.
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type Loader struct {
	client SSMAPI
	cache  map[string]map[string]any
	mu     sync.Mutex
	log    *zap.SugaredLogger
}

// New creates a Loader using the provided SSM client and logger.
func New(client SSMAPI, log *zap.SugaredLogger) *Loader {
	return &Loader{client: client, cache: make(map[string]map[string]any), log: log}
}

// Load fetches the profile with the given name from SSM, caching the result.
func (l *Loader) Load(ctx context.Context, name string) (map[string]any, error) {
	l.mu.Lock()
	if p, ok := l.cache[name]; ok {
		l.mu.Unlock()
		return p, nil
	}
	l.mu.Unlock()

	out, err := l.client.GetParameter(ctx, &ssm.GetParameterInput{Name: &name})
	if err != nil {
		return nil, fmt.Errorf("get parameter %s: %w", name, err)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(*out.Parameter.Value), &data); err != nil {
		return nil, fmt.Errorf("decode profile %s: %w", name, err)
	}

	l.mu.Lock()
	l.cache[name] = data
	l.mu.Unlock()
	return data, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

const chunkSize = 1000

type Parser interface {
	Parse(io.Reader) ([]map[string]string, error)
}

var (
	s3Client *s3.Client
	log      *zap.SugaredLogger
)

func loadParser(path string) (Parser, error) {
	p, err := plugin.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plugin: %w", err)
	}
	sym, err := p.Lookup("Parser")
	if err != nil {
		return nil, fmt.Errorf("lookup Parser: %w", err)
	}
	parser, ok := sym.(Parser)
	if !ok {
		return nil, fmt.Errorf("invalid parser type")
	}
	return parser, nil
}

func trimRows(rows []map[string]string) {
	for _, r := range rows {
		for k, v := range r {
			r[k] = strings.TrimSpace(v)
		}
	}
}

func handler(ctx context.Context, evt events.S3Event) error {
	rec := evt.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key
	obj, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("get object: %w", err)
	}
	defer obj.Body.Close()

	parser, err := loadParser("/opt/plugins/csv_pipe.so")
	if err != nil {
		return err
	}
	rows, err := parser.Parse(obj.Body)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	trimRows(rows)

	baseKey := strings.TrimSuffix(key, filepath.Ext(key))
	for i := 0; i < len(rows); i += chunkSize {
		end := i + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		var buf bytes.Buffer
		for _, r := range rows[i:end] {
			b, _ := json.Marshal(r)
			buf.Write(b)
			buf.WriteByte('\n')
		}
		outKey := fmt.Sprintf("%s_%d.jsonl", baseKey, i/chunkSize)
		if _, err := s3Client.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &outKey, Body: bytes.NewReader(buf.Bytes())}); err != nil {
			return fmt.Errorf("put chunk: %w", err)
		}
	}
	log.Infow("processed", "key", key, "rows", len(rows))
	return nil
}

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(err)
	}
	logger, _ := zap.NewProduction()
	log = logger.Sugar()
	s3Client = s3.NewFromConfig(cfg)
	lambda.Start(handler)
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

const (
	chunkSize = 1000
	maxMemory = 25 * 1024 * 1024
)

type parseFunc func(io.Reader) ([]map[string]string, error)

type s3API interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

var (
	s3Client s3API
	log      *zap.SugaredLogger
)

// getParserID returns the parser plug-in id from PARSER_ID or a default.
func getParserID() string {
	id := os.Getenv("PARSER_ID")
	if id == "" {
		id = "csv_pipe"
	}
	return id
}

// loadParser loads the parser plug-in with the given id.
func loadParser(id string) (parseFunc, error) {
	path := fmt.Sprintf("/opt/plugins/%s.so", id)
	p, err := plugin.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plugin: %w", err)
	}
	sym, err := p.Lookup("Parse")
	if err != nil {
		return nil, fmt.Errorf("lookup Parse: %w", err)
	}
	fn, ok := sym.(func(io.Reader) ([]map[string]string, error))
	if !ok {
		return nil, fmt.Errorf("invalid parser type")
	}
	return parseFunc(fn), nil
}

// trimRows trims whitespace from all values in the parsed rows.
func trimRows(rows []map[string]string) {
	for _, r := range rows {
		for k, v := range r {
			r[k] = strings.TrimSpace(v)
		}
	}
}

// profileSpec defines required columns loaded from PROFILE_JSON.
type profileSpec struct {
	Required []string `json:"required"`
}

// loadProfile decodes the PROFILE_JSON environment variable.
func loadProfile() (profileSpec, error) {
	v := os.Getenv("PROFILE_JSON")
	if v == "" {
		return profileSpec{}, nil
	}
	var p profileSpec
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return profileSpec{}, fmt.Errorf("decode profile: %w", err)
	}
	return p, nil
}

// validateHeader ensures the required columns exist in the header row.
func validateHeader(rows []map[string]string, req []string) error {
	if len(rows) == 0 {
		return fmt.Errorf("no rows")
	}
	for _, c := range req {
		if _, ok := rows[0][c]; !ok {
			return fmt.Errorf("missing column %s", c)
		}
	}
	return nil
}

// filterRows drops rows missing required columns and returns the valid rows
// and a count of invalid ones.
func filterRows(rows []map[string]string, req []string) ([]map[string]string, int) {
	var out []map[string]string
	bad := 0
	for _, r := range rows {
		valid := true
		for _, c := range req {
			if strings.TrimSpace(r[c]) == "" {
				valid = false
				break
			}
		}
		if valid {
			out = append(out, r)
		} else {
			bad++
		}
	}
	return out, bad
}

// Output is returned by the handler and either contains parsed rows or the
// S3 keys of chunked JSONL files along with a count of invalid rows.
type Output struct {
	Rows    []map[string]string `json:"rows,omitempty"`
	Keys    []string            `json:"keys,omitempty"`
	BadRows int                 `json:"badRows"`
}

// handler downloads an uploaded file, parses it using a plug-in and writes
// processed data back to S3 if the file is large.
func handler(ctx context.Context, evt events.S3Event) (Output, error) {
	rec := evt.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key
	size := rec.S3.Object.Size

	obj, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return Output{}, fmt.Errorf("get object: %w", err)
	}
	defer func() {
		if cerr := obj.Body.Close(); cerr != nil {
			log.Warnw("close body", "error", cerr)
		}
	}()

	parser, err := loadParser(getParserID())
	if err != nil {
		return Output{}, err
	}
	rows, err := parser(obj.Body)
	if err != nil {
		return Output{}, fmt.Errorf("parse: %w", err)
	}
	trimRows(rows)

	prof, err := loadProfile()
	if err != nil {
		return Output{}, err
	}
	if err := validateHeader(rows, prof.Required); err != nil {
		return Output{}, err
	}
	rows, bad := filterRows(rows, prof.Required)

	if size <= maxMemory {
		log.Infow("processed", "key", key, "rows", len(rows), "bad", bad)
		return Output{Rows: rows, BadRows: bad}, nil
	}

	baseKey := strings.TrimSuffix(key, filepath.Ext(key))
	var keys []string
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
			return Output{}, fmt.Errorf("put chunk: %w", err)
		}
		keys = append(keys, outKey)
	}
	log.Infow("processed", "key", key, "chunks", len(keys), "bad", bad)
	return Output{Keys: keys, BadRows: bad}, nil
}

// lambdaStart is overridden in tests to capture the handler start.
var lambdaStart = func(h interface{}) {
	lambda.Start(h)
}

var loadConfig = config.LoadDefaultConfig

// run loads configuration, sets up dependencies and starts the Lambda.
func run() error {
	cfg, err := loadConfig(context.Background())
	if err != nil {
		return err
	}
	logger, _ := zap.NewProduction()
	log = logger.Sugar()
	s3Client = s3.NewFromConfig(cfg)
	lambdaStart(handler)
	return nil
}

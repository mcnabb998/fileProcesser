package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

const maxSize = 50 * 1024 * 1024

var (
	s3Client  *s3.Client
	dbClient  *dynamodb.Client
	tableName = os.Getenv("MANIFEST_TABLE")
	log       *zap.SugaredLogger
)

func handler(ctx context.Context, evt events.S3Event) error {
	rec := evt.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key
	size := rec.S3.Object.Size
	if size > maxSize {
		return fmt.Errorf("file %s too large: %d", key, size)
	}

	obj, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("get object: %w", err)
	}
	defer func() {
		if cerr := obj.Body.Close(); cerr != nil {
			log.Warnw("close body", "error", cerr)
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(h, obj.Body); err != nil {
		return fmt.Errorf("read object: %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))

	_, err = dbClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &tableName,
		Item: map[string]types.AttributeValue{
			"FileKey":   &types.AttributeValueMemberS{Value: key},
			"SHA256":    &types.AttributeValueMemberS{Value: sum},
			"Processed": &types.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	log.Infow("manifest updated", "key", key, "sha", sum)
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
	dbClient = dynamodb.NewFromConfig(cfg)
	lambda.Start(handler)
}

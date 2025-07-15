package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"

	"github.com/your-org/file-processor-sample/internal/guard"
)

var (
	start      = lambda.Start
	loadConfig = config.LoadDefaultConfig
)

type s3API interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type ddbAPI interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

var (
	s3Client  s3API
	dbClient  guard.PutItemAPI
	tableName = os.Getenv("MANIFEST_TABLE")
	log       *zap.SugaredLogger
)

func handler(ctx context.Context, evt events.S3Event) error {
	rec := evt.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key
	size := rec.S3.Object.Size

	if err := guard.ValidateSize(key, size); err != nil {
		return err
	}

	obj, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("get object: %w", err)
	}
	defer guard.Close(obj.Body, log)

	sum, err := guard.ComputeSHA256(obj.Body)
	if err != nil {
		return fmt.Errorf("read object: %w", err)
	}

	if err := guard.PutManifest(ctx, dbClient, tableName, key, sum); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	log.Infow("manifest updated", "key", key, "sha", sum)
	return nil
}

func main() {
	cfg, err := loadConfig(context.Background())
	if err != nil {
		panic(err)
	}
	logger, _ := zap.NewProduction()
	log = logger.Sugar()
	s3Client = s3.NewFromConfig(cfg)
	dbClient = dynamodb.NewFromConfig(cfg)
	start(handler)
}

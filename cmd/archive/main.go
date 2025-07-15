package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

var (
	s3Client *s3.Client
	dbClient *dynamodb.Client
	cwClient *cloudwatch.Client
	table    = os.Getenv("MANIFEST_TABLE")
	log      *zap.SugaredLogger
)

func handler(ctx context.Context, evt events.S3Event) error {
	rec := evt.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key
	archiveKey := "archive/" + key
	_, err := s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &bucket,
		CopySource: aws.String(bucket + "/" + key),
		Key:        &archiveKey,
	})
	if err != nil {
		return fmt.Errorf("copy object: %w", err)
	}
	_, err = dbClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &table,
		Key: map[string]dbtypes.AttributeValue{
			"FileKey": &dbtypes.AttributeValueMemberS{Value: key},
		},
		UpdateExpression: aws.String("SET Processed = :t"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":t": &dbtypes.AttributeValueMemberBOOL{Value: true},
		},
	})
	if err != nil {
		return fmt.Errorf("update manifest: %w", err)
	}
	_, err = cwClient.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String("FileProcessor"),
		MetricData: []cwtypes.MetricDatum{
			{MetricName: aws.String("Processed"), Value: aws.Float64(1)},
		},
	})
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	log.Infow("archived", "key", key)
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
	cwClient = cloudwatch.NewFromConfig(cfg)
	lambda.Start(handler)
}

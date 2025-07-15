package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"go.uber.org/zap"
)

type s3API interface {
	GetObjectTagging(context.Context, *s3.GetObjectTaggingInput, ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error)
	CopyObject(context.Context, *s3.CopyObjectInput, ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	PutObjectTagging(context.Context, *s3.PutObjectTaggingInput, ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error)
}

type dbAPI interface {
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

type cwAPI interface {
	PutMetricData(context.Context, *cloudwatch.PutMetricDataInput, ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricDataOutput, error)
}

var (
	s3Client s3API
	dbClient dbAPI
	cwClient cwAPI
	table    = os.Getenv("MANIFEST_TABLE")
	log      *zap.SugaredLogger
	now      = time.Now
)

// ArchiveEvent is triggered after a file has been parsed and contains
// the S3 event along with import statistics.
type ArchiveEvent struct {
	events.S3Event
	RowsProcessed int `json:"rowsProcessed"`
	RowsFailed    int `json:"rowsFailed"`
}

// handler archives the source file, updates DynamoDB and emits metrics.
func handler(ctx context.Context, evt ArchiveEvent) error {
	rec := evt.Records[0]
	bucket := rec.S3.Bucket.Name
	key := rec.S3.Object.Key

	tagOut, err := s3Client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("get tagging: %w", err)
	}
	for _, t := range tagOut.TagSet {
		if strings.EqualFold(aws.ToString(t.Key), "processed") && strings.EqualFold(aws.ToString(t.Value), "true") {
			log.Infow("already archived", "key", key)
			return nil
		}
	}

	archiveKey := fmt.Sprintf("archive/%s/%s", now().UTC().Format("2006/01/02"), key)
	start := time.Now()
	_, err = s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &bucket,
		CopySource: aws.String(bucket + "/" + key),
		Key:        &archiveKey,
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "SlowDown" {
			time.Sleep(200 * time.Millisecond)
			if _, err = s3Client.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &bucket, CopySource: aws.String(bucket + "/" + key), Key: &archiveKey}); err != nil {
				return fmt.Errorf("copy object retry: %w", err)
			}
		} else {
			return fmt.Errorf("copy object: %w", err)
		}
	}
	latency := time.Since(start).Milliseconds()

	_, err = s3Client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket:  &bucket,
		Key:     &key,
		Tagging: &s3types.Tagging{TagSet: []s3types.Tag{{Key: aws.String("processed"), Value: aws.String("true")}}},
	})
	if err != nil {
		return fmt.Errorf("tag source: %w", err)
	}

	_, err = dbClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &table,
		Key: map[string]dbtypes.AttributeValue{
			"FileKey": &dbtypes.AttributeValueMemberS{Value: key},
		},
		UpdateExpression:         aws.String("SET rowsProcessed=:rp, rowsFailed=:rf, #S=:s"),
		ExpressionAttributeNames: map[string]string{"#S": "status"},
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":rp": &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(evt.RowsProcessed)},
			":rf": &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(evt.RowsFailed)},
			":s":  &dbtypes.AttributeValueMemberS{Value: "ARCHIVED"},
		},
		ConditionExpression: aws.String("attribute_not_exists(#S) OR #S <> :s"),
	})
	if err != nil {
		var cfe *dbtypes.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return fmt.Errorf("manifest already archived")
		}
		return fmt.Errorf("update manifest: %w", err)
	}

	_, err = cwClient.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace: aws.String("FileProcessor"),
		MetricData: []cwtypes.MetricDatum{
			{MetricName: aws.String("RowsProcessed"), Value: aws.Float64(float64(evt.RowsProcessed))},
			{MetricName: aws.String("RowsFailed"), Value: aws.Float64(float64(evt.RowsFailed))},
			{MetricName: aws.String("ArchiveLatencyMs"), Value: aws.Float64(float64(latency)), Unit: cwtypes.StandardUnitMilliseconds},
		},
	})
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	log.Infow("archived", "key", key, "dest", archiveKey)
	return nil
}

// main configures AWS clients and starts the Lambda handler.
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

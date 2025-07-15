//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

// startCmd runs c and returns its stdout string
func startCmd(ctx context.Context, c *exec.Cmd) (string, error) {
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestWorkflow(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("E2E env not set")
	}
	ctx := context.Background()

	// start LocalStack
	lsID, err := startCmd(ctx, exec.Command("docker", "run", "-d", "-p", "4566:4566", "localstack/localstack"))
	if err != nil {
		t.Fatalf("start localstack: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", lsID).Run()

	// start Step Functions Local
	sfnID, err := startCmd(ctx, exec.Command("docker", "run", "-d", "-p", "8083:8083", "amazon/aws-stepfunctions-local"))
	if err != nil {
		t.Fatalf("start sfn local: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", sfnID).Run()

	// start WireMock
	wmID, err := startCmd(ctx, exec.Command("docker", "run", "-d", "-p", "8080:8080", "wiremock/wiremock"))
	if err != nil {
		t.Fatalf("start wiremock: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", wmID).Run()

	endpoint := "http://localhost:4566"
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: endpoint, HostnameImmutable: true}, nil
			})),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	s3c := s3.NewFromConfig(cfg)
	ddbc := dynamodb.NewFromConfig(cfg)
	cw := cloudwatch.NewFromConfig(cfg)
	sfnc := sfn.NewFromConfig(cfg, func(o *sfn.Options) {
		o.EndpointOptions.DisableHTTPS = true
		o.BaseEndpoint = aws.String("http://localhost:8083")
	})

	// create resources
	_, err = s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("source")})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	_, err = ddbc.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("Manifest"),
		AttributeDefinitions: []dbtypes.AttributeDefinition{{AttributeName: aws.String("FileKey"), AttributeType: dbtypes.ScalarAttributeTypeS}},
		KeySchema:            []dbtypes.KeySchemaElement{{AttributeName: aws.String("FileKey"), KeyType: dbtypes.KeyTypeHash}},
		BillingMode:          dbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// deploy state machine
	def := `{"StartAt":"Hello","States":{"Hello":{"Type":"Pass","End":true}}}`
	sm, err := sfnc.CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       aws.String("BatchWrapper"),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/Dummy"),
		Definition: aws.String(def),
	})
	if err != nil {
		t.Fatalf("create machine: %v", err)
	}

	// upload sample file
	sample, err := os.ReadFile("testdata/sample.qns")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("source"), Key: aws.String("sample.qns"), Body: bytes.NewReader(sample)})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	// start execution
	input := struct {
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}{"source", "sample.qns"}
	b, _ := json.Marshal(input)
	execOut, err := sfnc.StartExecution(ctx, &sfn.StartExecutionInput{StateMachineArn: sm.StateMachineArn, Input: aws.String(string(b))})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}

	// wait for success
	for i := 0; i < 60; i++ {
		out, err := sfnc.DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: execOut.ExecutionArn})
		if err != nil {
			t.Fatalf("describe exec: %v", err)
		}
		if out.Status == sfn.ExecutionStatusSucceeded {
			break
		}
		if out.Status == sfn.ExecutionStatusFailed {
			t.Fatalf("execution failed")
		}
		time.Sleep(1 * time.Second)
	}

	// check dynamo item
	dOut, err := ddbc.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String("Manifest"), Key: map[string]dbtypes.AttributeValue{"FileKey": &dbtypes.AttributeValueMemberS{Value: "sample.qns"}}})
	if err != nil || len(dOut.Item) == 0 {
		t.Fatalf("item missing: %v", err)
	}

	// check metrics
	_, err = cw.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{MetricData: []cwtypes.MetricDatum{{MetricName: aws.String("RowsProcessed"), Value: aws.Float64(1)}}})
	if err != nil {
		t.Fatalf("metric put: %v", err)
	}
}

func TestWorkflowCSV(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("E2E env not set")
	}
	ctx := context.Background()

	// start LocalStack
	lsID, err := startCmd(ctx, exec.Command("docker", "run", "-d", "-p", "4566:4566", "localstack/localstack"))
	if err != nil {
		t.Fatalf("start localstack: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", lsID).Run()

	// start Step Functions Local
	sfnID, err := startCmd(ctx, exec.Command("docker", "run", "-d", "-p", "8083:8083", "amazon/aws-stepfunctions-local"))
	if err != nil {
		t.Fatalf("start sfn local: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", sfnID).Run()

	endpoint := "http://localhost:4566"
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: endpoint, HostnameImmutable: true}, nil
			})),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	s3c := s3.NewFromConfig(cfg)
	sfnc := sfn.NewFromConfig(cfg, func(o *sfn.Options) {
		o.EndpointOptions.DisableHTTPS = true
		o.BaseEndpoint = aws.String("http://localhost:8083")
	})

	// create bucket
	_, err = s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("source")})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	// deploy simple state machine returning counts
	def := `{"StartAt":"Result","States":{"Result":{"Type":"Pass","Result":{"rowsProcessed":2,"rowsFailed":1},"End":true}}}`
	sm, err := sfnc.CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       aws.String("CSVFlow"),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/Dummy"),
		Definition: aws.String(def),
	})
	if err != nil {
		t.Fatalf("create machine: %v", err)
	}

	// upload csv sample
	sample, err := os.ReadFile("testdata/flood_qns_sample.csv")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("source"), Key: aws.String("flood_qns_sample.csv"), Body: bytes.NewReader(sample)})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	// start execution
	input := struct {
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}{"source", "flood_qns_sample.csv"}
	b, _ := json.Marshal(input)
	execOut, err := sfnc.StartExecution(ctx, &sfn.StartExecutionInput{StateMachineArn: sm.StateMachineArn, Input: aws.String(string(b))})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}

	// wait for success
	var execRes *sfn.DescribeExecutionOutput
	for i := 0; i < 60; i++ {
		execRes, err = sfnc.DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: execOut.ExecutionArn})
		if err != nil {
			t.Fatalf("describe exec: %v", err)
		}
		if execRes.Status == sfn.ExecutionStatusSucceeded {
			break
		}
		if execRes.Status == sfn.ExecutionStatusFailed {
			t.Fatalf("execution failed")
		}
		time.Sleep(1 * time.Second)
	}

	if execRes == nil || execRes.Output == nil {
		t.Fatalf("no output")
	}
	var res struct {
		RowsProcessed int `json:"rowsProcessed"`
		RowsFailed    int `json:"rowsFailed"`
	}
	if err := json.Unmarshal([]byte(*execRes.Output), &res); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if res.RowsProcessed != 2 || res.RowsFailed != 1 {
		t.Fatalf("unexpected counts: %+v", res)
	}
}

package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

const (
	awsEndpoint   = "http://localhost:4566"
	sfnEndpoint   = "http://localhost:8083"
	wiremockURL   = "http://localhost:8080"
	manifestTable = "Manifest"
)

func zipLambda(code string) []byte {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	f, _ := zw.Create("main.py")
	_, _ = f.Write([]byte(code))
	_ = zw.Close()
	return buf.Bytes()
}

func createLambda(ctx context.Context, c *lambda.Client, name, code string) (string, error) {
	out, err := c.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimePython311,
		Handler:      aws.String("main.handler"),
		Role:         aws.String("arn:aws:iam::000000000000:role/Dummy"),
		Code:         &lambdatypes.FunctionCode{ZipFile: zipLambda(code)},
		Environment: &lambdatypes.Environment{Variables: map[string]string{
			"AWS_ENDPOINT":   awsEndpoint,
			"MANIFEST_TABLE": manifestTable,
			"WIREMOCK":       wiremockURL,
		}},
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.FunctionArn), nil
}

func waitExec(ctx context.Context, c *sfn.Client, arn string) error {
	for i := 0; i < 60; i++ {
		out, err := c.DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: aws.String(arn)})
		if err != nil {
			return err
		}
		if out.Status == sfntypes.ExecutionStatusSucceeded {
			return nil
		}
		if out.Status == sfntypes.ExecutionStatusFailed {
			return fmt.Errorf("execution failed")
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("timeout")
}

func TestE2E(t *testing.T) {
	if os.Getenv("E2E") == "" {
		t.Skip("E2E env not set")
	}
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	s3c := s3.NewFromConfig(cfg)
	ddbc := dynamodb.NewFromConfig(cfg)
	lmb := lambda.NewFromConfig(cfg, func(o *lambda.Options) {
		o.BaseEndpoint = aws.String(awsEndpoint)
		o.EndpointOptions.DisableHTTPS = true
	})
	cw := cloudwatch.NewFromConfig(cfg, func(o *cloudwatch.Options) {
		o.BaseEndpoint = aws.String(awsEndpoint)
		o.EndpointOptions.DisableHTTPS = true
	})
	sfnc := sfn.NewFromConfig(cfg, func(o *sfn.Options) {
		o.BaseEndpoint = aws.String(sfnEndpoint)
		o.EndpointOptions.DisableHTTPS = true
	})

	// resources
	_, _ = s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("source")})
	_, _ = ddbc.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(manifestTable),
		AttributeDefinitions: []dbtypes.AttributeDefinition{{AttributeName: aws.String("FileKey"), AttributeType: dbtypes.ScalarAttributeTypeS}},
		KeySchema:            []dbtypes.KeySchemaElement{{AttributeName: aws.String("FileKey"), KeyType: dbtypes.KeyTypeHash}},
		BillingMode:          dbtypes.BillingModePayPerRequest,
	})

	// wiremock stub
	resp, err := http.Post(wiremockURL+"/__admin/mappings", "application/json", bytes.NewBufferString(`{"request":{"method":"POST","url":"/sobjects/Import_Error__c"},"response":{"status":201}}`))
	if err != nil {
		t.Fatalf("http.Post failed: %v", err)
	}
	defer resp.Body.Close()

	guardCode := `import boto3,hashlib,os
s3=boto3.client('s3',endpoint_url=os.environ['AWS_ENDPOINT'])
ddb=boto3.client('dynamodb',endpoint_url=os.environ['AWS_ENDPOINT'])
TABLE=os.environ['MANIFEST_TABLE']

def handler(event,context):
	obj=s3.get_object(Bucket=event['bucket'],Key=event['key'])
	data=obj['Body'].read()
	sha=hashlib.sha256(data).hexdigest()
	ddb.put_item(TableName=TABLE,Item={'FileKey':{'S':event['key']},'SHA256':{'S':sha}})
	return event
`
	parseCode := `import boto3,csv,io,os
s3=boto3.client('s3',endpoint_url=os.environ['AWS_ENDPOINT'])

def handler(event,context):
	obj=s3.get_object(Bucket=event['bucket'],Key=event['key'])
	rows=list(csv.DictReader(io.StringIO(obj['Body'].read().decode()),delimiter='|'))
	good=[r for r in rows if '@' in r.get('email','')]
	bad=[{'External_Row_Id__c':str(i+1),'message':'bad email'} for i,r in enumerate(rows) if '@' not in r.get('email','')]
	return {'bucket':event['bucket'],'key':event['key'],'rowsProcessed':len(good),'rowsFailed':len(bad),'errors':bad}
`
	logCode := `import os,json,urllib.request
URL=os.environ['WIREMOCK']+'/sobjects/Import_Error__c'

def handler(event,context):
	req=urllib.request.Request(URL,data=json.dumps(event).encode(),headers={'Content-Type':'application/json'})
	r=urllib.request.urlopen(req)
	return {'status':r.status}
`
	archiveCode := `import boto3,os
cw=boto3.client('cloudwatch',endpoint_url=os.environ['AWS_ENDPOINT'])
ddb=boto3.client('dynamodb',endpoint_url=os.environ['AWS_ENDPOINT'])
TABLE=os.environ['MANIFEST_TABLE']

def handler(event,context):
	ddb.update_item(TableName=TABLE,Key={'FileKey':{'S':event['key']}},UpdateExpression='SET rowsProcessed=:rp, rowsFailed=:rf',ExpressionAttributeValues={':rp':{'N':str(event['rowsProcessed'])},':rf':{'N':str(event['rowsFailed'])}})
	cw.put_metric_data(Namespace='FileProcessor',MetricData=[{'MetricName':'RowsFailed','Value':float(event['rowsFailed'])}])
	return event
`

	guardArn, err := createLambda(ctx, lmb, "Guard", guardCode)
	if err != nil {
		t.Fatalf("create guard: %v", err)
	}
	parseArn, err := createLambda(ctx, lmb, "Parse", parseCode)
	if err != nil {
		t.Fatalf("create parse: %v", err)
	}
	logArn, err := createLambda(ctx, lmb, "Log", logCode)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	archiveArn, err := createLambda(ctx, lmb, "Archive", archiveCode)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}

	def := fmt.Sprintf(`{"StartAt":"Guard","States":{"Guard":{"Type":"Task","Resource":"%s","Next":"Parse"},"Parse":{"Type":"Task","Resource":"%s","ResultPath":"$","Next":"Log"},"Log":{"Type":"Map","ItemsPath":"$.errors","Iterator":{"StartAt":"Send","States":{"Send":{"Type":"Task","Resource":"%s","End":true}}},"Next":"Archive"},"Archive":{"Type":"Task","Resource":"%s","End":true}}}`, guardArn, parseArn, logArn, archiveArn)

	sm, err := sfnc.CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       aws.String("Wrapper"),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/Dummy"),
		Definition: aws.String(def),
	})
	if err != nil {
		t.Fatalf("create sm: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join("testdata", "flood_qns_sample.csv"))
	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("source"), Key: aws.String("flood_qns_sample.csv"), Body: bytes.NewReader(data)})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	in := map[string]string{"bucket": "source", "key": "flood_qns_sample.csv"}
	b, _ := json.Marshal(in)
	execOut, err := sfnc.StartExecution(ctx, &sfn.StartExecutionInput{StateMachineArn: sm.StateMachineArn, Input: aws.String(string(b))})
	if err != nil {
		t.Fatalf("start exec: %v", err)
	}
	if err := waitExec(ctx, sfnc, aws.ToString(execOut.ExecutionArn)); err != nil {
		t.Fatalf("exec wait: %v", err)
	}

	dOut, err := ddbc.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(manifestTable), Key: map[string]dbtypes.AttributeValue{"FileKey": &dbtypes.AttributeValueMemberS{Value: "flood_qns_sample.csv"}}})
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	rp, _ := strconv.Atoi(dOut.Item["rowsProcessed"].(*dbtypes.AttributeValueMemberN).Value)
	rf, _ := strconv.Atoi(dOut.Item["rowsFailed"].(*dbtypes.AttributeValueMemberN).Value)
	if rp != 2 || rf != 1 {
		t.Fatalf("counts unexpected: %d %d", rp, rf)
	}

	resp, err = http.Get(wiremockURL + "/__admin/requests?method=POST&url=/sobjects/Import_Error__c")
	if err != nil {
		t.Fatalf("wiremock reqs: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var reqs struct {
		Requests []any `json:"requests"`
	}
	_ = json.Unmarshal(body, &reqs)
	if len(reqs.Requests) != 1 {
		t.Fatalf("import error calls: %d", len(reqs.Requests))
	}

	mOut, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("FileProcessor"),
		MetricName: aws.String("RowsFailed"),
		StartTime:  aws.Time(time.Now().Add(-5 * time.Minute)),
		EndTime:    aws.Time(time.Now()),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	})
	if err != nil {
		t.Fatalf("metric stats: %v", err)
	}
	if len(mOut.Datapoints) == 0 || int(*mOut.Datapoints[0].Sum) != 1 {
		t.Fatalf("metric value not 1")
	}
}

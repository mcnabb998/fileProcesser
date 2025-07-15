//go:build lambda

package main

import (
	"context"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(err)
	}
	logger, _ := zap.NewProduction()
	log := logger.Sugar()
	table := os.Getenv("AUTH_TABLE")
	if table == "" {
		table = "SfAuthToken"
	}
	pk := os.Getenv("APP_ID") + "#" + os.Getenv("ENV")
	store := &dynamoStore{table: table, pk: pk, db: dynamodb.NewFromConfig(cfg)}
	broker := &Broker{
		store:      store,
		cw:         cloudwatch.NewFromConfig(cfg),
		httpClient: http.DefaultClient,
		sfURL:      os.Getenv("SF_TOKEN_URL"),
		creds: map[string]string{
			"client_id":     os.Getenv("SF_CLIENT_ID"),
			"client_secret": os.Getenv("SF_CLIENT_SECRET"),
			"username":      os.Getenv("SF_USERNAME"),
			"password":      os.Getenv("SF_PASSWORD"),
			"grant_type":    "password",
		},
		log: log,
	}
	lambda.Start(broker.handler)
}

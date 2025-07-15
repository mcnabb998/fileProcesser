//go:build lambda

package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type dynamoAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// dynamoStore persists tokens in DynamoDB for sharing across functions.
type dynamoStore struct {
	table string
	pk    string
	db    dynamoAPI
}

// Get retrieves the token record and its expiry from DynamoDB.
func (d *dynamoStore) Get(ctx context.Context) (string, time.Time, bool, error) {
	out, err := d.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table,
		Key: map[string]dbtypes.AttributeValue{
			"PK": &dbtypes.AttributeValueMemberS{Value: d.pk},
		},
	})
	if err != nil {
		return "", time.Time{}, false, err
	}
	if out.Item == nil {
		return "", time.Time{}, false, nil
	}
	tokAttr, ok := out.Item["Token"].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return "", time.Time{}, false, nil
	}
	expAttr, ok := out.Item["ExpiresAt"].(*dbtypes.AttributeValueMemberN)
	var exp time.Time
	if ok {
		if v, err := strconv.ParseInt(expAttr.Value, 10, 64); err == nil {
			exp = time.Unix(v, 0)
		}
	}
	refAttr, _ := out.Item["Refreshing"].(*dbtypes.AttributeValueMemberBOOL)
	refreshing := false
	if refAttr != nil {
		refreshing = refAttr.Value
	}
	return tokAttr.Value, exp, refreshing, nil
}

// TryLock attempts to mark the token as refreshing using a conditional write.
func (d *dynamoStore) TryLock(ctx context.Context) (bool, error) {
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table,
		Key: map[string]dbtypes.AttributeValue{
			"PK": &dbtypes.AttributeValueMemberS{Value: d.pk},
		},
		UpdateExpression:    aws.String("SET Refreshing = :t"),
		ConditionExpression: aws.String("attribute_not_exists(Refreshing) OR Refreshing = :f"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":t": &dbtypes.AttributeValueMemberBOOL{Value: true},
			":f": &dbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		var ccfe *dbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Save stores the new token and expiration time in DynamoDB.
func (d *dynamoStore) Save(ctx context.Context, token string, exp time.Time) error {
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table,
		Key: map[string]dbtypes.AttributeValue{
			"PK": &dbtypes.AttributeValueMemberS{Value: d.pk},
		},
		UpdateExpression: aws.String("SET Token = :tok, ExpiresAt = :exp, Refreshing = :f"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tok": &dbtypes.AttributeValueMemberS{Value: token},
			":exp": &dbtypes.AttributeValueMemberN{Value: strconv.FormatInt(exp.Unix(), 10)},
			":f":   &dbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	return err
}

// Unlock clears the refreshing flag in DynamoDB.
func (d *dynamoStore) Unlock(ctx context.Context) error {
	_, err := d.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.table,
		Key: map[string]dbtypes.AttributeValue{
			"PK": &dbtypes.AttributeValueMemberS{Value: d.pk},
		},
		UpdateExpression: aws.String("SET Refreshing = :f"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":f": &dbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	return err
}

package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"
)

// MaxSize is the maximum allowed file size in bytes.
const MaxSize int64 = 50 * 1024 * 1024

// PutItemAPI abstracts the DynamoDB PutItem operation.
type PutItemAPI interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// ValidateSize returns an error if the provided size exceeds MaxSize.
func ValidateSize(key string, size int64) error {
	if size > MaxSize {
		return fmt.Errorf("file %s too large: %d", key, size)
	}
	return nil
}

// ComputeSHA256 reads from r and returns its SHA-256 hex digest.
func ComputeSHA256(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PutManifest writes the file key and checksum to DynamoDB.
func PutManifest(ctx context.Context, db PutItemAPI, table, key, sum string) error {
	_, err := db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &table,
		Item: map[string]types.AttributeValue{
			"FileKey":   &types.AttributeValueMemberS{Value: key},
			"SHA256":    &types.AttributeValueMemberS{Value: sum},
			"Processed": &types.AttributeValueMemberBOOL{Value: false},
		},
	})
	return err
}

// Close closes c and logs any returned error.
func Close(c io.Closer, log *zap.SugaredLogger) {
	if err := c.Close(); err != nil {
		log.Warnw("close body", "error", err)
	}
}

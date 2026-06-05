package s3

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	sharedmodel "shopnexus-server/internal/shared/model"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type ClientImpl struct {
	client *s3.Client
	config S3Config
}

type S3Config struct {
	AccessKeyID       string
	SecretAccessKey   string
	Region            string
	Bucket            string
	CloudfrontURL     string
	DefaultPresignTTL time.Duration
}

// NewClient initializes a new S3 client using application configuration.
func NewClient(cfg S3Config) (*ClientImpl, error) {
	// Create custom credentials provider
	credProvider := credentials.NewStaticCredentialsProvider(
		cfg.AccessKeyID,
		cfg.SecretAccessKey,
		"", // Session token is optional and usually not needed for regular access keys
	)

	// Load AWS configuration with custom credentials
	awsCfg, err := awsConfig.LoadDefaultConfig(
		context.Background(),
		awsConfig.WithRegion(cfg.Region),
		awsConfig.WithCredentialsProvider(credProvider),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	return &ClientImpl{
		client: s3.NewFromConfig(awsCfg),
		config: cfg,
	}, nil
}

func (s *ClientImpl) Config() sharedmodel.Option {
	return sharedmodel.Option{
		ID:          "s3",
		Name:        "Amazon S3",
		Provider:    "AWS",
		Description: "Amazon S3 Object Storage",
	}
}

func (s *ClientImpl) GetURL(ctx context.Context, key string) (string, error) {
	// Check if the file is private
	if strings.HasPrefix(key, "private/") {
		// Return presigned URL with 30 minutes expiration for private files
		return s.GetPresignedURL(ctx, key, s.config.DefaultPresignTTL)
	}

	// Return CloudFront URL for public files
	if s.config.CloudfrontURL == "" {
		return "", stderrors.New("cloudfront URL is not configured")
	}

	return fmt.Sprintf("https://%s/%s", s.config.CloudfrontURL, key), nil
}

func (s *ClientImpl) GetPresignedURL(ctx context.Context, key string, expireIn time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client)

	request, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.config.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expireIn))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return request.URL, nil
}

func (s *ClientImpl) Upload(ctx context.Context, key string, body io.Reader, private bool) (string, error) {
	prefix := "public/"
	if private {
		prefix = "private/"
	}

	if !strings.HasPrefix(key, prefix) {
		key = prefix + key
	}

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.config.Bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to S3: %w", err)
	}

	return key, nil
}

func (s *ClientImpl) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.config.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file from S3: %w", err)
	}

	return nil
}

func (s *ClientImpl) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.config.Bucket),
		Prefix: aws.String(prefix),
	}

	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects from S3: %w", err)
		}

		for _, obj := range page.Contents {
			keys = append(keys, *obj.Key)
		}
	}

	return keys, nil
}

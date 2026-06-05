package commonbiz

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/objectstore"
	objlocal "shopnexus-server/internal/infras/objectstore/local"
	objremote "shopnexus-server/internal/infras/objectstore/remote"
	objs3 "shopnexus-server/internal/infras/objectstore/s3"
	accountmodel "shopnexus-server/internal/module/account/model"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	sharedmodel "shopnexus-server/internal/shared/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

// SetupObjectStore registers the available object-store options in the DB
// catalog. Clients themselves are constructed on-demand per call —
// nothing is cached here.
func (b *CommonHandler) SetupObjectStore() error {
	return b.upsertOptions(context.Background(), UpsertOptionsParams{
		Type:    string(sharedmodel.OptionTypeObjectStore),
		Configs: b.objectstoreConfigs(),
	})
}

// objectstoreConfigs lists all available object-store options. Cheap to call:
// only "local" and "remote" build a real client; "s3" is described inline.
func (b *CommonHandler) objectstoreConfigs() []sharedmodel.Option {
	configs := make([]sharedmodel.Option, 0, 3)

	if local, err := objlocal.NewClient(objlocal.LocalConfig{Root: "./tmp/uploads", BaseURL: ""}); err == nil {
		configs = append(configs, local.Config())
	}
	configs = append(configs, sharedmodel.Option{
		ID:          "s3",
		Name:        "Amazon S3",
		Provider:    "AWS",
		Description: "Amazon S3 Object Storage",
	})
	configs = append(configs, objremote.NewClient(objremote.RemoteConfig{}).Config())

	return configs
}

// getPlaceholderURL returns the configured 404 placeholder image URL, if any.
func (b *CommonHandler) getPlaceholderURL() string {
	return b.cfg.Filestore.Placeholder404Url
}

// mustGetObjectStore builds a fresh object-store client per call.
// On unknown providers or s3 init failure, falls back to local.
func (b *CommonHandler) mustGetObjectStore(provider string) objectstore.Client {
	switch provider {
	case "s3":
		s3, err := objs3.NewClient(objs3.S3Config{
			AccessKeyID:     b.cfg.Filestore.S3.AccessKeyID,
			SecretAccessKey: b.cfg.Filestore.S3.SecretAccessKey,
			Region:          b.cfg.Filestore.S3.Region,
			Bucket:          b.cfg.Filestore.S3.Bucket,
			CloudfrontURL:   b.cfg.Filestore.S3.CloudfrontURL,
		})
		if err != nil {
			b.logger.Warn("init s3 objectstore", slog.Any("error", err))
			return b.mustGetObjectStore("local")
		}
		return s3
	case "remote":
		return objremote.NewClient(objremote.RemoteConfig{})
	default:
		local, err := objlocal.NewClient(objlocal.LocalConfig{Root: "./tmp/uploads", BaseURL: ""})
		if err != nil {
			b.logger.Warn("init local objectstore", slog.Any("error", err))
			return nil
		}
		return local
	}
}

type UploadFileParams struct {
	Account     accountmodel.AuthenticatedAccount
	File        io.Reader `validate:"required"`
	Filename    string    `validate:"required"`
	ContentType string    `validate:"required"`
	Size        int64     `validate:"required"`
	Private     bool      `validate:"omitempty"`
}

type UploadFileResult struct {
	ResourceID uuid.UUID
	Provider   string
	ObjectKey  string
	URL        string
}

// UploadFile stores a single uploaded file to the configured object store
// and creates a corresponding resource record.
// UploadFile is called directly by the transport layer (not via Restate)
// because io.Reader cannot be serialized through the Restate ingress.
func (b *CommonHandler) UploadFile(ctx context.Context, params UploadFileParams) (UploadFileResult, error) {
	var zero UploadFileResult

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("invalid upload params: %w", err)
	}

	store := b.mustGetObjectStore(b.cfg.Filestore.Type)
	myKey := fmt.Sprintf("%s_%s", uuid.New().String(), params.Filename)

	objectKey, err := store.Upload(ctx, myKey, params.File, params.Private)
	if err != nil {
		return zero, fmt.Errorf("upload local: %w", err)
	}

	resource, err := b.storage.Querier().CreateDefaultResource(ctx, commondb.CreateDefaultResourceParams{
		Provider:     b.cfg.Filestore.Type,
		ObjectKey:    objectKey,
		UploadedByID: uuid.NullUUID{UUID: params.Account.ID, Valid: true},
		Mime:         params.ContentType,
		Size:         params.Size,
		Metadata:     []byte("{}"),
	})
	if err != nil {
		return zero, fmt.Errorf("insert resource: %w", err)
	}

	url, err := store.GetURL(ctx, objectKey)
	if err != nil {
		return zero, fmt.Errorf("get file url: %w", err)
	}

	return UploadFileResult{
		ResourceID: resource.ID,
		Provider:   b.cfg.Filestore.Type,
		ObjectKey:  objectKey,
		URL:        url,
	}, nil
}

type GetFileURLParams struct {
	Provider  string
	ObjectKey string
}

func (b *CommonHandler) GetFileURL(ctx restate.Context, params GetFileURLParams) (string, error) {
	url, err := b.mustGetObjectStore(params.Provider).GetURL(ctx, params.ObjectKey)
	if err != nil {
		return "", fmt.Errorf("get file url: %w", err)
	}

	return url, nil
}

// mustGetFileURL returns the URL for an object key, falling back to a placeholder on error.
func (b *CommonHandler) mustGetFileURL(ctx context.Context, provider string, objectKey string) string {
	url, err := b.mustGetObjectStore(provider).GetURL(ctx, objectKey)
	if err != nil {
		b.logger.Error(
			"failed to get file url for object key",
			slog.String("object_key", objectKey),
			slog.String("provider", provider),
			slog.Any("error", err),
		)
		return b.getPlaceholderURL()
	}

	return url
}

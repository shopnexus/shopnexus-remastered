package local

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	sharedmodel "shopnexus-server/internal/shared/model"
	"strings"
	"time"
)

type ClientImpl struct {
	root    string
	baseURL string
}

type LocalConfig struct {
	// Root is the base directory where objects are stored on disk (e.g., ./uploads)
	Root string
	// BaseURL is the public base URL used to construct public URLs, e.g., http://localhost:8080/api/v1/shared/files
	// If empty, GetURL will return an error and Upload(private=false) will return the key only.
	BaseURL string
}

func NewClient(cfg LocalConfig) (*ClientImpl, error) {
	if cfg.Root == "" {
		return nil, stderrors.New("local root is required")
	}
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create root: %w", err)
	}
	return &ClientImpl{root: cfg.Root, baseURL: strings.TrimRight(cfg.BaseURL, "/")}, nil
}

func (c *ClientImpl) fullPath(key string) string {
	// prevent path traversal
	clean := filepath.Clean(key)
	return filepath.Join(c.root, clean)
}

func (c *ClientImpl) Config() sharedmodel.Option {
	return sharedmodel.Option{
		ID:          "local",
		Name:        "Local Storage",
		Provider:    "Local",
		Description: "Local File System Storage",
	}
}

func (c *ClientImpl) GetURL(ctx context.Context, key string) (string, error) {
	_ = ctx
	if c.baseURL == "" {
		return "", stderrors.New("baseURL not configured for local objectstore")
	}
	return fmt.Sprintf("%s/%s", c.baseURL, key), nil
}

func (c *ClientImpl) GetPresignedURL(ctx context.Context, key string, _ time.Duration) (string, error) {
	// For local, just return the public URL (no signing).
	return c.GetURL(ctx, key)
}

func (c *ClientImpl) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	_ = ctx
	var keys []string
	rootWithPrefix := c.fullPath(prefix)
	// if prefix is a file, return single element if exists
	if info, err := os.Stat(rootWithPrefix); err == nil && !info.IsDir() {
		return []string{prefix}, nil
	}
	err := filepath.WalkDir(c.fullPath("."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(c.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, prefix) {
			keys = append(keys, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (c *ClientImpl) Upload(ctx context.Context, key string, reader io.Reader, private bool) (string, error) {
	_ = ctx
	prefix := "public/"
	if private {
		prefix = "private/"
	}
	if !strings.HasPrefix(key, prefix) {
		key = prefix + key
	}
	path := c.fullPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, reader); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return key, nil
}

func (c *ClientImpl) Delete(ctx context.Context, key string) error {
	_ = ctx
	path := c.fullPath(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file: %w", err)
	}
	return nil
}

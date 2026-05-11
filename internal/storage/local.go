package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create local root: %w", err)
	}
	return &Local{root: root}, nil
}

func (l *Local) Put(_ context.Context, key string, reader io.Reader, _ int64, contentType string) error {
	target := l.resolve(key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create object dir: %w", err)
	}
	tmp := target + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp object: %w", err)
	}
	if _, err := io.Copy(file, reader); err != nil {
		file.Close()
		return fmt.Errorf("write object: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close object: %w", err)
	}
	if err := os.WriteFile(target+".meta", []byte(contentType), 0o644); err != nil {
		return fmt.Errorf("write object metadata: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("commit object: %w", err)
	}
	return nil
}

func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	target := l.resolve(key)
	file, err := os.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectInfo{}, ErrObjectNotFound
		}
		return nil, ObjectInfo{}, fmt.Errorf("open object: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, ObjectInfo{}, fmt.Errorf("stat object: %w", err)
	}
	contentType := "application/octet-stream"
	if meta, err := os.ReadFile(target + ".meta"); err == nil && len(meta) > 0 {
		contentType = strings.TrimSpace(string(meta))
	}
	return file, ObjectInfo{
		Key:         key,
		Size:        info.Size(),
		ContentType: contentType,
	}, nil
}

func (l *Local) Stat(_ context.Context, key string) (ObjectInfo, error) {
	target := l.resolve(key)
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectInfo{}, ErrObjectNotFound
		}
		return ObjectInfo{}, fmt.Errorf("stat object: %w", err)
	}
	contentType := "application/octet-stream"
	if meta, err := os.ReadFile(target + ".meta"); err == nil && len(meta) > 0 {
		contentType = strings.TrimSpace(string(meta))
	}
	return ObjectInfo{
		Key:         key,
		Size:        info.Size(),
		ContentType: contentType,
	}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	target := l.resolve(key)
	_ = os.Remove(target + ".meta")
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (l *Local) DeletePrefix(_ context.Context, prefix string) error {
	target := l.resolve(prefix)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("delete prefix: %w", err)
	}
	return nil
}

func (l *Local) resolve(key string) string {
	key = filepath.Clean(strings.ReplaceAll(key, "\\", "/"))
	key = strings.TrimPrefix(key, "/")
	return filepath.Join(l.root, key)
}

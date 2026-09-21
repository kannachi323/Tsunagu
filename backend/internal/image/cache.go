package image

import (
	"bytes"
	"context"
	"fmt"
	_ "golang.org/x/image/webp"
	stdimage "image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ExtFromContentType(ct string) string {
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}

func DownloadToFile(url, destDir, destName string) (string, error) {
	return DownloadToFileContext(context.Background(), url, destDir, destName)
}
func DownloadToFileContext(ctx context.Context, url, destDir, destName string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s: status %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return "", err
	}
	return SaveBytesToFile(data, resp.Header.Get("Content-Type"), destDir, destName)
}

func SaveBytesToFile(data []byte, contentType, destDir, destName string) (string, error) {
	if err := Validate(data); err != nil {
		return "", err
	}
	contentType = http.DetectContentType(data)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("creating dir %s: %w", destDir, err)
	}
	ext := ExtFromContentType(contentType)
	path := filepath.Join(destDir, destName+ext)
	file, err := os.CreateTemp(destDir, ".image-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func ClearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("removing %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Limit transient decoder memory independently of the encoded-byte caches.
var validationSlots = make(chan struct{}, 2)

// Validate rejects corrupt images and decompression bombs before caching them.
func Validate(data []byte) error {
	if len(data) == 0 || len(data) > 32<<20 {
		return fmt.Errorf("invalid image size")
	}
	config, _, err := stdimage.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid source image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return fmt.Errorf("image exceeds 16 megapixel decode limit")
	}
	validationSlots <- struct{}{}
	defer func() { <-validationSlots }()
	if _, _, err := stdimage.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("corrupt source image: %w", err)
	}
	return nil
}

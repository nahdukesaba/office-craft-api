package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"office-craft-api/internal/config"
)

const (
	storageResponseLimit  = 4096
	MaxStorageUploadBytes = 10 * 1024 * 1024
)

// StorageService accesses Supabase Storage from the backend.
// Its privileged credential must never be returned to the browser.
type StorageService struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewStorageService(cfg *config.Config) *StorageService {
	base := strings.TrimRight(strings.TrimSpace(cfg.SupabaseURL), "/")
	base = strings.TrimSuffix(base, "/storage/v1")
	base = strings.TrimRight(base, "/")

	return &StorageService{
		baseURL: base,
		key:     strings.TrimSpace(cfg.SupabaseServiceKey),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *StorageService) ready() error {
	if s.key == "" || s.baseURL == "" {
		return errors.New("server-side storage is not configured")
	}

	u, err := url.Parse(s.baseURL)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("invalid storage endpoint")
	}

	local := u.Hostname() == "localhost" ||
		u.Hostname() == "127.0.0.1"

	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("invalid storage endpoint")
	}

	return nil
}

func hasInvalidPathSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." ||
		strings.ContainsAny(seg, "/\\?#\x00") {
		return true
	}

	unescaped, err := url.PathUnescape(seg)
	if err != nil || unescaped == "" || unescaped == "." || unescaped == ".." ||
		strings.ContainsAny(unescaped, "/\\?#\x00") {
		return true
	}

	return false
}

func storagePath(bucket, path string) (string, error) {
	bucket = strings.TrimSpace(bucket)
	path = strings.TrimSpace(path)

	if hasInvalidPathSegment(bucket) ||
		path == "" ||
		strings.HasPrefix(path, "/") ||
		strings.HasSuffix(path, "/") {
		return "", errors.New("invalid storage bucket or object key")
	}

	parts := strings.Split(path, "/")

	for i, part := range parts {
		if hasInvalidPathSegment(part) {
			return "", errors.New("invalid storage object key")
		}
		parts[i] = url.PathEscape(part)
	}

	return url.PathEscape(bucket) + "/" +
		strings.Join(parts, "/"), nil
}

func (s *StorageService) do(
	ctx context.Context,
	method, target, contentType string,
	body io.Reader,
	extraHeaders map[string]string,
) (*http.Response, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx, method, s.baseURL+target, body,
	)
	if err != nil {
		return nil, errors.New("failed to construct storage request")
	}

	req.Header.Set("apikey", s.key)
	req.Header.Set("Authorization", "Bearer "+s.key)

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.New("storage service request failed")
	}

	return resp, nil
}

func (s *StorageService) Upload(
	ctx context.Context,
	bucket, path, mime string,
	file []byte,
) error {
	if len(file) == 0 || len(file) > MaxStorageUploadBytes {
		return errors.New("invalid storage object size")
	}

	mime = strings.TrimSpace(mime)
	if mime == "" {
		return errors.New("storage content type is required")
	}

	object, err := storagePath(bucket, path)
	if err != nil {
		return err
	}

	resp, err := s.do(
		ctx,
		http.MethodPost,
		"/storage/v1/object/"+object,
		mime,
		bytes.NewReader(file),
		map[string]string{"x-upsert": "false"},
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(
		io.Discard,
		io.LimitReader(resp.Body, storageResponseLimit),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf(
			"storage upload failed: HTTP %d",
			resp.StatusCode,
		)
	}

	return nil
}

func (s *StorageService) Delete(
	ctx context.Context,
	bucket, path string,
) error {
	object, err := storagePath(bucket, path)
	if err != nil {
		return err
	}

	resp, err := s.do(
		ctx,
		http.MethodDelete,
		"/storage/v1/object/"+object,
		"",
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(
		io.Discard,
		io.LimitReader(resp.Body, storageResponseLimit),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf(
			"storage delete failed: HTTP %d",
			resp.StatusCode,
		)
	}

	return nil
}

func (s *StorageService) SignedURL(
	ctx context.Context,
	bucket, path string,
) (string, error) {
	object, err := storagePath(bucket, path)
	if err != nil {
		return "", err
	}

	resp, err := s.do(
		ctx,
		http.MethodPost,
		"/storage/v1/object/sign/"+object,
		"application/json",
		strings.NewReader(`{"expiresIn":3600}`),
		nil,
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(
			io.Discard,
			io.LimitReader(resp.Body, storageResponseLimit),
		)
		return "", fmt.Errorf(
			"storage signed URL failed: HTTP %d",
			resp.StatusCode,
		)
	}

	var result struct {
		SignedURL string `json:"signedURL"`
		SignedUrl string `json:"signedUrl"`
	}

	err = json.NewDecoder(
		io.LimitReader(resp.Body, storageResponseLimit),
	).Decode(&result)
	if err != nil {
		return "", errors.New("invalid signed URL response")
	}

	signed := result.SignedURL
	if signed == "" {
		signed = result.SignedUrl
	}
	if signed == "" {
		return "", errors.New("storage did not return a signed URL")
	}

	parsed, err := url.Parse(signed)
	if err != nil {
		return "", errors.New("invalid signed URL")
	}

	if parsed.IsAbs() {
		base, _ := url.Parse(s.baseURL)
		if parsed.Host != base.Host ||
			parsed.Scheme != base.Scheme {
			return "", errors.New("unexpected signed URL host")
		}
		return signed, nil
	}

	if strings.HasPrefix(signed, "/storage/v1/object/sign/") {
		return s.baseURL + signed, nil
	}

	if strings.HasPrefix(signed, "/object/sign/") {
		return s.baseURL + "/storage/v1" + signed, nil
	}

	return "", errors.New("unexpected signed URL path")
}

func (s *StorageService) PublicURL(
	bucket, path string,
) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}

	object, err := storagePath(bucket, path)
	if err != nil {
		return "", err
	}

	return s.baseURL +
		"/storage/v1/object/public/" + object, nil
}

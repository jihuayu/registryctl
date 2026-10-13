package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jihuayu/registryctl/internal/model"
	"github.com/jihuayu/registryctl/internal/oci"
)

type Status struct {
	Session model.Session `json:"session"`
	Parts   []model.Part  `json:"parts"`
}

func (c *Client) Status(ctx context.Context, id string) (Status, error) {
	var state Status
	_, e := c.Request(ctx, "GET", "/api/registry/v1/uploads/"+id, nil, &state, nil)
	return state, e
}
func (c *Client) PushBlob(ctx context.Context, repo string, blob oci.Blob, progress func(string)) (model.Session, error) {
	status, headErr := c.Request(ctx, "HEAD", "/v2/"+repo+"/blobs/"+blob.Descriptor.Digest.String(), nil, nil, nil)
	if headErr == nil && status == 200 {
		progress("复用已有镜像层 " + blob.Descriptor.Digest.String())
		return model.Session{Repository: repo, Digest: blob.Descriptor.Digest.String(), Size: blob.Descriptor.Size, State: "committed"}, nil
	}
	var failure Error
	if headErr != nil && (!errors.As(headErr, &failure) || failure.Status != 404) {
		return model.Session{}, headErr
	}
	key, e := resumeKey(c.Base, repo, blob.Descriptor.Digest.String(), false)
	if e != nil {
		return model.Session{}, e
	}
	var s model.Session
	_, e = c.Request(ctx, "POST", "/api/registry/v1/uploads", model.Create{Repository: repo, Digest: blob.Descriptor.Digest.String(), Size: blob.Descriptor.Size, MediaType: blob.Descriptor.MediaType}, &s, map[string]string{"Idempotency-Key": key})
	if e != nil {
		return s, e
	}
	if s.State == "expired" || s.State == "aborted" || s.State == "rejected" || (!s.Expires.IsZero() && time.Now().After(s.Expires) && s.State != "committed" && s.State != "committing") {
		key, e = resumeKey(c.Base, repo, blob.Descriptor.Digest.String(), true)
		if e != nil {
			return s, e
		}
		_, e = c.Request(ctx, "POST", "/api/registry/v1/uploads", model.Create{Repository: repo, Digest: blob.Descriptor.Digest.String(), Size: blob.Descriptor.Size, MediaType: blob.Descriptor.MediaType}, &s, map[string]string{"Idempotency-Key": key})
		if e != nil {
			return s, e
		}
	}
	progress("会话 " + s.ID)
	return c.ResumeBlob(ctx, s.ID, blob, progress)
}
func resumeKey(base, repo, digest string, renew bool) (string, error) {
	root, e := os.UserCacheDir()
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(base + "\x00" + repo + "\x00" + digest))
	dir := filepath.Join(root, "registryctl", "uploads")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	path := filepath.Join(dir, hex.EncodeToString(sum[:]))
	if renew {
		key := model.Random()
		temp := path + "." + model.Random()[:12]
		if e = os.WriteFile(temp, []byte(key), 0600); e != nil {
			return "", e
		}
		if e = os.Rename(temp, path); e != nil {
			_ = os.Remove(temp)
			return "", e
		}
		return key, nil
	}
	b, e := os.ReadFile(path)
	if e == nil {
		return string(b), nil
	}
	if !os.IsNotExist(e) {
		return "", e
	}
	key := model.Random()
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(e) {
		b, e = os.ReadFile(path)
		return string(b), e
	}
	if e != nil {
		return "", e
	}
	_, e = f.WriteString(key)
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	return key, e
}
func (c *Client) ResumeBlob(ctx context.Context, id string, blob oci.Blob, progress func(string)) (model.Session, error) {
	status, e := c.Status(ctx, id)
	if e != nil {
		return model.Session{}, e
	}
	s := status.Session
	if s.Digest != blob.Descriptor.Digest.String() || s.Size != blob.Descriptor.Size {
		return s, errors.New("恢复源内容与上传会话不匹配")
	}
	if s.State == "committed" {
		return s, nil
	}
	if s.State == "uploading" {
		done := map[int]bool{}
		for _, p := range status.Parts {
			expected := s.PartSize
			if p.Number == s.PartCount {
				expected = s.Size - int64(p.Number-1)*s.PartSize
			}
			if p.Size == expected {
				done[p.Number] = true
			}
		}
		jobs := make(chan int)
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		var mu sync.Mutex
		var first error
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for n := range jobs {
					if workCtx.Err() != nil {
						return
					}
					if err := c.uploadPart(workCtx, s, blob.Path, n); err != nil {
						mu.Lock()
						if first == nil {
							first = err
						}
						mu.Unlock()
						cancel()
						return
					}
					mu.Lock()
					progress(fmt.Sprintf("分片 %d/%d 已上传", n, s.PartCount))
					mu.Unlock()
				}
			}()
		}
	send:
		for n := 1; n <= s.PartCount; n++ {
			if done[n] {
				continue
			}
			select {
			case jobs <- n:
			case <-workCtx.Done():
				break send
			}
		}
		close(jobs)
		wg.Wait()
		if first != nil {
			return s, first
		}
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
	}
	if s.State == "uploading" || s.State == "sealing" {
		_, e = c.Request(ctx, "POST", "/api/registry/v1/uploads/"+id+"/seal", nil, &s, nil)
		if e != nil {
			return s, e
		}
	}
	progress("等待 NAS 校验镜像摘要")
	for s.State == "verifying" {
		select {
		case <-ctx.Done():
			return s, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		state, e := c.Status(ctx, id)
		if e != nil {
			return s, e
		}
		s = state.Session
	}
	if s.State != "verified" && s.State != "committing" && s.State != "committed" {
		return s, fmt.Errorf("上传不能提交：%s %s", s.State, s.LastError)
	}
	_, e = c.Request(ctx, "POST", "/api/registry/v1/uploads/"+id+"/commit", nil, &s, nil)
	return s, e
}
func (c *Client) uploadPart(ctx context.Context, s model.Session, path string, n int) error {
	size := s.PartSize
	if n == s.PartCount {
		size = s.Size - int64(n-1)*s.PartSize
	}
	for attempt := 0; attempt < 3; attempt++ {
		var parts []model.SignedPart
		_, e := c.Request(ctx, "POST", "/api/registry/v1/uploads/"+s.ID+"/parts/sign", map[string]any{"numbers": []int{n}}, &parts, nil)
		if e != nil {
			return e
		}
		if len(parts) != 1 || parts[0].Number != n {
			return errors.New("invalid signing response")
		}
		if e = ObjectURL(parts[0].URL); e != nil {
			return e
		}
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		reader := io.NewSectionReader(file, int64(n-1)*s.PartSize, size)
		r, e := http.NewRequestWithContext(ctx, "PUT", parts[0].URL, reader)
		if e != nil {
			_ = file.Close()
			return errors.New("invalid R2 signing response")
		}
		r.ContentLength = size
		for k, v := range parts[0].Headers {
			r.Header.Set(k, v)
		}
		resp, e := HTTPClient().Do(r)
		_ = file.Close()
		if e == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return errors.New("R2 分片上传失败，可按会话恢复")
}
func (c *Client) Manifest(ctx context.Context, repo, ref, media string, body []byte) error {
	token, e := c.Token(ctx)
	if e != nil {
		return e
	}
	r, e := http.NewRequestWithContext(ctx, "PUT", c.Base+"/v2/"+repo+"/manifests/"+ref, bytes.NewReader(body))
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", media)
	r.Header.Set("User-Agent", "registryctl/0.1")
	resp, e := c.HTTP.Do(r)
	if e != nil {
		return errors.New("Manifest connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		return fmt.Errorf("Manifest publish failed (%s)", strconv.Itoa(resp.StatusCode))
	}
	return nil
}

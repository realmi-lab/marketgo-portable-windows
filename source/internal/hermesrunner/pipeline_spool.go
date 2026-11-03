package hermesrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"market-share-go/internal/notice"
)

var errNoticeSpoolFull = errors.New("notice spool capacity reached; collection paused")

type noticeSpool struct {
	dir   string
	limit int64
	mu    sync.Mutex
}

func newNoticeSpool(dir string, limit int64) (*noticeSpool, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("notice spool must be an explicit absolute persistent directory")
	}
	if limit < 2*notice.PipelineMaxObjectBytes {
		return nil, errors.New("notice spool capacity too small")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &noticeSpool{dir: dir, limit: limit}, nil
}

func (s *noticeSpool) capacity(reserve int64) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var used int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		used += info.Size()
	}
	if used+reserve > s.limit {
		return errNoticeSpoolFull
	}
	return nil
}

func (s *noticeSpool) save(req notice.PipelineRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	if err = s.capacity(int64(len(data))); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, notice.PipelineID()+".json")
	if err = os.Rename(temporary, path); err != nil {
		return "", err
	}
	return path, s.syncDirectory()
}

func (s *noticeSpool) syncDirectory() error {
	f, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *noticeSpool) flush(ctx context.Context, send func(context.Context, notice.PipelineRequest) (notice.PipelineResponse, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".json") && !strings.HasPrefix(entry.Name(), ".pending-")) {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 2*notice.PipelineMaxObjectBytes {
			return errors.New("invalid oversized notice spool entry")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var req notice.PipelineRequest
		if err = json.Unmarshal(raw, &req); err != nil {
			return err
		}
		req.Operation = "object"
		if req.Object == nil || notice.PipelineBytesDigest(req.Object.Bytes) != req.Object.SHA256 {
			return errors.New("notice spool checksum mismatch")
		}
		out, err := send(ctx, req)
		if err != nil {
			return err
		}
		if out.Object == nil || out.Object.ID == "" || out.Object.SHA256 != req.Object.SHA256 {
			return errors.New("notice object receipt mismatch")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		if err = s.syncDirectory(); err != nil {
			return err
		}
	}
	return nil
}

func (s *noticeSpool) archive(ctx context.Context, job notice.PipelineJob, raw []byte, mediaType string, send func(context.Context, notice.PipelineRequest) (notice.PipelineResponse, error)) (string, error) {
	req := notice.PipelineRequest{Operation: "object", RequestID: notice.PipelineID(), Exchange: job.Exchange, JobID: job.ID, SourceGeneration: job.Generation, Object: &notice.PipelineObject{Bytes: raw, SHA256: notice.PipelineBytesDigest(raw), MediaType: mediaType}}
	path, err := s.save(req)
	if err != nil {
		return "", err
	}
	out, err := send(ctx, req)
	if err != nil {
		return "", err
	}
	if out.Object == nil || out.Object.ID == "" || out.Object.SHA256 != req.Object.SHA256 {
		return "", fmt.Errorf("invalid notice object receipt")
	}
	if err = os.Remove(path); err != nil {
		return "", err
	}
	return out.Object.ID, s.syncDirectory()
}

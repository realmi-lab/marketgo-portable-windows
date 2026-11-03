package alignedstage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const evidenceReadLimit = 4 << 20

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func readJSON(path string, v any) error {
	data, err := readEvidence(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func readEvidence(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > evidenceReadLimit {
		return nil, errors.New("stage evidence must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, evidenceReadLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > evidenceReadLimit {
		return nil, errors.New("stage evidence exceeds size bound")
	}
	return data, nil
}
func writeJSON(path string, v any, immutable bool) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if immutable {
		old, err := os.ReadFile(path)
		if err == nil {
			if !bytes.Equal(old, data) {
				return errors.New("immutable stage evidence conflict")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

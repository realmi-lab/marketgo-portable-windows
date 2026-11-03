package hermesrunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrMarketQuestionInputCapture = errors.New("required market question input capture failed")

// Directory creation and path resolution happen before the invoker can write
// an input file. Required QA capture must fail closed at these boundaries too.
func marketQuestionCaptureSetupError(config MarketQuestionConfig, err error) error {
	if config.CaptureInput {
		return fmt.Errorf("%w: %w", ErrMarketQuestionInputCapture, err)
	}
	return err
}

// Optional QA evidence only. Never overwrite another call's input, follow a
// symlink, or write a prompt into execution metadata or public diagnostics.
func captureMarketQuestionInput(dir string, callID int64, prompt []byte) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("market question input directory must be private")
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-input.txt", callID))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(prompt); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

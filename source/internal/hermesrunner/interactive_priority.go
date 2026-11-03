package hermesrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	interactiveQuestionMarkerName = ".interactive-notice-question"
	// Interactive questions may use the full five-minute Service
	// budget. Keep a crashed lease bounded, but never let a live question lose
	// priority before that budget and start competing with background analysis.
	interactiveQuestionMarkerTTL = 6 * time.Minute
	interactiveQuestionPoll      = 100 * time.Millisecond
)

var errHermesAnalysisPreempted = errors.New("Hermes background analysis preempted by an interactive question")

func interactiveQuestionMarkerPath(outputDir string) string {
	// Containers keep private scratch/checkpoint roots but may share only this
	// lease directory. Both question writers and background readers must resolve
	// the same override; changing the output root must not silently split them.
	root := strings.TrimSpace(os.Getenv("MARKET_SHARE_HERMES_PRIORITY_DIR"))
	if root == "" {
		root = strings.TrimSpace(outputDir)
	}
	if root == "" {
		root = filepath.Join(".tmp", "hermes-notices")
	}
	return filepath.Join(root, interactiveQuestionMarkerName)
}

func beginInteractiveQuestionPriority(outputDir string) (func(), error) {
	path := interactiveQuestionMarkerPath(outputDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Hermes priority directory: %w", err)
	}
	// Each question owns a distinct lease. Market and notice requests (and
	// separate processes sharing an output directory) must not erase one
	// another's priority when they complete in either order. Keep the original
	// marker path: older background readers also recognize this directory via
	// os.Stat, while this reader checks individual lease expiry.
	for attempt := 0; attempt < 3; attempt++ {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create Hermes priority leases: %w", err)
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect Hermes priority leases: %w", err)
		}
		if !info.IsDir() {
			// Preserve a still-live marker left by an older executable. Never
			// replace another request's lease during a mixed-version rollout.
			if time.Since(info.ModTime()) <= interactiveQuestionMarkerTTL {
				return nil, errors.New("legacy interactive Hermes question is still active")
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove expired Hermes priority marker: %w", err)
			}
			continue
		}
		lease, err := os.CreateTemp(path, "request-*")
		if errors.Is(err, os.ErrNotExist) {
			// The last releaser may remove an empty directory between Mkdir
			// and CreateTemp. Retry; removing a nonempty directory is atomic
			// and fails, so no active lease can be lost this way.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mark interactive Hermes question: %w", err)
		}
		leasePath := lease.Name()
		if err := lease.Close(); err != nil {
			_ = os.Remove(leasePath)
			return nil, fmt.Errorf("close Hermes priority lease: %w", err)
		}
		var releaseOnce sync.Once
		return func() {
			releaseOnce.Do(func() {
				_ = os.Remove(leasePath)
				_ = syscall.Rmdir(path) // Never remove a replacement legacy file.
			})
		}, nil
	}
	return nil, errors.New("Hermes priority directory changed while acquiring a lease")
}

// BeginInteractiveQuestionPriority pauses and preempts background notice model
// work while a user-facing market or notice question is being answered.
func BeginInteractiveQuestionPriority(outputDir string) (func(), error) {
	return beginInteractiveQuestionPriority(outputDir)
}

func interactiveQuestionActive(outputDir string, now time.Time) bool {
	path := interactiveQuestionMarkerPath(outputDir)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		if now.Sub(info.ModTime()) <= interactiveQuestionMarkerTTL {
			return true
		}
		_ = os.Remove(path)
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	active := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "request-") {
			continue
		}
		lease, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(lease.ModTime()) <= interactiveQuestionMarkerTTL {
			active = true
		} else {
			_ = os.Remove(filepath.Join(path, entry.Name()))
		}
	}
	if !active {
		_ = syscall.Rmdir(path)
	}
	return active
}

func waitForInteractiveQuestion(ctx context.Context, outputDir string) error {
	for interactiveQuestionActive(outputDir, time.Now()) {
		timer := time.NewTimer(interactiveQuestionPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func runBackgroundHermesCommand(ctx context.Context, outputDir string, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if interactiveQuestionActive(outputDir, time.Now()) {
		return errHermesAnalysisPreempted
	}
	configureProcessTree(command)
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(interactiveQuestionPoll)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			terminateProcessTree(command.Process)
			<-done
			return ctx.Err()
		case <-ticker.C:
			if interactiveQuestionActive(outputDir, time.Now()) {
				terminateProcessTree(command.Process)
				<-done
				return errHermesAnalysisPreempted
			}
		}
	}
}

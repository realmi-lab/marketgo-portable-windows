package hermesrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type WorkerConfig struct {
	Bin                          string
	BackendURL                   string
	Token                        string
	HermesBin                    string
	VisionOCRBin                 string
	OutputDir                    string
	Interval                     time.Duration
	MaxAnalysesPerExchangePerRun int
}

const workerGracefulShutdownTimeout = 5 * time.Second

func SuperviseWorker(ctx context.Context, config WorkerConfig, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	worker, err := resolveWorkerBin(config.Bin)
	if err != nil {
		logger.Error("Hermes notice worker is unavailable", "error", err)
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		command := exec.Command(worker)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		command.Env = append(cleanChildEnvironment(),
			"MARKET_SHARE_HERMES_BACKEND_URL="+config.BackendURL,
			"MARKET_SHARE_HERMES_NOTICE_INGEST_TOKEN="+config.Token,
			"HERMES_BIN="+config.HermesBin,
			"MARKET_SHARE_VISION_OCR_BIN="+config.VisionOCRBin,
			"MARKET_SHARE_HERMES_NOTICE_OUTPUT_DIR="+config.OutputDir,
			"MARKET_SHARE_NOTICE_POLL_SEC="+strconv.FormatFloat(config.Interval.Seconds(), 'f', -1, 64),
			"MARKET_SHARE_HERMES_WORKER_GOMAXPROCS=1",
			"MARKET_SHARE_HERMES_WORKER_MEMORY_MIB=256",
			"MARKET_SHARE_HERMES_MAX_ANALYSES_PER_EXCHANGE_PER_RUN="+strconv.Itoa(
				max(1, config.MaxAnalysesPerExchangePerRun),
			),
		)
		logger.Info("starting isolated Hermes notice worker", "binary", worker)
		err = runSupervisedWorkerCommand(ctx, command)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		logger.Error("Hermes notice worker stopped; restarting", "error", err, "backoff", backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func runSupervisedWorkerCommand(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	configureProcessTree(command)
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}

	// The worker owns nested Hermes process groups. Give it SIGTERM first so
	// signal.NotifyContext can cancel the active pipeline and kill those groups
	// itself. Killing only the worker group immediately can orphan a nested
	// Hermes leader because it intentionally has a different PGID.
	if signalProcessForGracefulShutdown(command.Process) {
		timer := time.NewTimer(workerGracefulShutdownTimeout)
		defer timer.Stop()
		select {
		case <-done:
			return ctx.Err()
		case <-timer.C:
		}
	}
	terminateProcessTree(command.Process)
	<-done
	return ctx.Err()
}

func resolveWorkerBin(explicit string) (string, error) {
	candidates := []string{strings.TrimSpace(explicit)}
	if executable, err := os.Executable(); err == nil {
		name := "market-share-hermes-worker"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("worker binary %q was not found", strings.TrimSpace(explicit))
}

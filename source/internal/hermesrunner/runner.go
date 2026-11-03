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

	"market-share-go/internal/notice"
)

const (
	maxLogOutputBytes = 4_000
)

type Config struct {
	Fetcher                      notice.HermesEvidenceFetcher
	LoadExisting                 func(context.Context) (notice.Snapshot, error)
	Submit                       func(context.Context, notice.HermesIngestRequest) (notice.HermesIngestResult, error)
	HermesBin                    string
	VisionOCRBin                 string
	OutputDir                    string
	Interval                     time.Duration
	MaxAnalysesPerExchangePerRun int
	DisableModelAnalysis         bool
	// CollectorOnly keeps collection independent of model and OCR runtimes.
	CollectorOnly               bool
	CorpusRange                 *notice.HermesCorpusRange
	CorpusExchange              string
	MaxDetailsPerExchangePerRun int
	TargetedReanalysis          *TargetedReanalysis
}

// ResolveVisionOCRBin locates the optional macOS Vision helper. Absence is not
// fatal because non-macOS deployments and unbundled development builds keep
// using the existing Hermes vision path.
func ResolveVisionOCRBin(explicit string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	candidates := []string{strings.TrimSpace(explicit)}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "marketgo-vision-ocr"))
	}
	candidates = append(candidates,
		"/Applications/MarketGo Launcher.app/Contents/Resources/marketgo-vision-ocr",
	)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

// ResolveHermesBin removes a launchd/PATH dependency from the isolated worker.
// An explicitly configured path remains fail-closed; only the default bare
// command may fall back to the current user's conventional local bin path.
func ResolveHermesBin(explicit string) (string, error) {
	userHome, _ := os.UserHomeDir()
	return resolveHermesBin(explicit, userHome)
}

// ResolveCodexBinDir selects the app-server Codex runtime used by Hermes'
// openai-codex provider. A configured directory is fail-closed. Without one,
// prefer the bundled per-user runtime before an arbitrary PATH command.
func ResolveCodexBinDir(explicit string) (string, error) {
	userHome, _ := os.UserHomeDir()
	return resolveCodexBinDir(explicit, userHome, os.Getenv("LOCALAPPDATA"), runtime.GOOS)
}

func resolveCodexBinDir(explicit, userHome, localAppData, goos string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	binaryName := "codex"
	if goos == "windows" {
		binaryName = "codex.exe"
	}
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		switch goos {
		case "windows":
			if strings.TrimSpace(localAppData) != "" {
				candidates = append(candidates, filepath.Join(
					localAppData, "hermes", "codex-runtime", "node_modules", "@openai",
					"codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin",
				))
			}
		default:
			if strings.TrimSpace(userHome) != "" {
				candidates = append(candidates, filepath.Join(userHome, ".codex", "plugins", ".plugin-appserver"))
			}
		}
	}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, statErr := os.Stat(filepath.Join(absolute, binaryName)); statErr == nil && !info.IsDir() {
			return absolute, nil
		}
	}
	if explicit == "" {
		if executable, err := exec.LookPath(binaryName); err == nil {
			return filepath.Dir(executable), nil
		}
	}
	return "", fmt.Errorf("Codex runtime directory %q was not found", explicit)
}

func resolveHermesBin(explicit, userHome string) (string, error) {
	configured := strings.TrimSpace(explicit)
	if configured == "" {
		configured = "hermes"
	}
	candidates := []string{configured}
	if configured == "hermes" && strings.TrimSpace(userHome) != "" {
		candidates = append(candidates, filepath.Join(userHome, ".local", "bin", "hermes"))
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("Hermes executable %q was not found", configured)
}

func Run(ctx context.Context, config Config, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if config.TargetedReanalysis != nil {
		logger.Error("targeted reanalysis requires ExecuteOnce, not a scheduled worker")
		return
	}
	execute := func(runCtx context.Context) error {
		return executeOnce(runCtx, config)
	}
	runSchedule(ctx, config.Interval, func(runCtx context.Context) {
		started := time.Now()
		logger.Info("Hermes notice run started")
		if err := execute(runCtx); err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Error("Hermes notice run failed", "error", err, "elapsed", time.Since(started))
			}
			return
		}
		logger.Info("Hermes notice run completed", "elapsed", time.Since(started))
	})
}

func runSchedule(ctx context.Context, interval time.Duration, execute func(context.Context)) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	execute(ctx)
	if ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			execute(ctx)
		}
	}
}

func executeOnce(ctx context.Context, config Config) error {
	if err := validateTargetedReanalysisConfig(config); err != nil {
		return err
	}
	if config.TargetedReanalysis != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	if config.CorpusRange != nil {
		if err := config.CorpusRange.Validate(time.Now()); err != nil {
			return err
		}
		if !notice.IsDomesticExchange(config.CorpusExchange) {
			return errors.New("corpus run requires one explicit domestic exchange")
		}
	}
	collector, err := notice.NewHermesSourceCollector(config.Fetcher)
	if err != nil {
		return err
	}
	if config.LoadExisting == nil || config.Submit == nil {
		return errors.New("Hermes notice load and submit callbacks are required")
	}
	pipeline := newPipeline(config, collector)
	return pipeline.run(ctx)
}

func runCommand(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	configureProcessTree(command)
	if err := command.Start(); err != nil {
		return err
	}
	if trace := questionTrace(ctx); trace != nil {
		now := time.Now().UTC()
		trace.ExecStartedAt = &now
		trace.Result = "RUNNING"
		writeQuestionTrace(command.Dir, trace)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	select {
	case err := <-done:
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	case <-ctx.Done():
		terminateProcessTree(command.Process)
		<-done
		return ctx.Err()
	}
}

func terminateProcessTree(process *os.Process) {
	if process == nil {
		return
	}
	if terminateProcessGroup(process) {
		return
	}
	if name, arguments := processTreeTermination(runtime.GOOS, process.Pid); name != "" {
		if err := exec.Command(name, arguments...).Run(); err == nil {
			return
		}
	}
	_ = process.Kill()
}

func processTreeTermination(goos string, pid int) (string, []string) {
	if goos == "windows" && pid > 0 {
		return "taskkill", []string{"/PID", strconv.Itoa(pid), "/T", "/F"}
	}
	return "", nil
}

func tail(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	return value[len(value)-maximum:]
}

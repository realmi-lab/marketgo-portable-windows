package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const DefaultDatabaseURL = "postgresql://127.0.0.1:5432/market_share"

const (
	StoragePostgres     = "postgres"
	StorageSQLiteMemory = "sqlite-memory"
)

const (
	DeploymentStandalone = "standalone"
	DeploymentActive     = "active"
	DeploymentStandby    = "standby"
	DeploymentManaged    = "managed"
)

const (
	maximumQueueSize          = 262_144
	maximumRefreshSeconds     = 24 * 60 * 60
	minimumRegistryRefreshSec = 10 * 60
	maximumRegistryRefreshSec = 30 * 60
	maximumFXAgeMilliseconds  = 24 * 60 * 60 * 1_000
	maximumOrderBookStaleSec  = 60 * 60
	maximumShutdownSeconds    = 5 * 60
	maximumReferenceOrderKRW  = 1_000_000_000_000_000
	maximumPriceGapQuarantine = 1_000_000
)

type Config struct {
	CollectorURL                             string
	CollectorToken                           string
	DatabaseURL                              string
	StorageMode                              string
	AggregationEngine                        string
	APIHost                                  string
	APIPort                                  int
	LogLevel                                 slog.Level
	RegistryRefresh                          time.Duration
	BinanceTickerRefresh                     time.Duration
	NoticeEnabled                            bool
	NoticeSources                            []string
	NoticePoll                               time.Duration
	HermesNoticeIngestToken                  string
	NoticePipelineV2Enabled                  bool
	HermesNoticeAutostart                    bool
	HermesNoticeWorkerBin                    string
	HermesNoticeBin                          string
	VisionOCRBin                             string
	HermesNoticeOutputDir                    string
	HermesNoticeMaxAnalysesPerExchangePerRun int
	LLMEnabled                               bool
	QuestionLearningEnabled                  bool
	QuestionACEnabled                        bool
	QuestionEvidenceRetentionDays            int
	DevelopmentQuestionTimeout               time.Duration
	DevelopmentQuestionCaptureInput          bool
	QueueSize                                int
	FXMaxAge                                 time.Duration
	ReferenceOrderKRW                        float64
	OrderBookStale                           time.Duration
	PriceGapQuarantineBPS                    float64
	ContractIdentities                       map[string]marketanalysis.ContractIdentityProof
	ShutdownTimeout                          time.Duration
	MemoryLimitMiB                           int
	DeploymentMode                           string
	DeploymentInstance                       string
	DeploymentControlToken                   string
	DeploymentBufferMiB                      int
	DeploymentActiveFile                     string
	DeploymentInitialActive                  string
}

func (c Config) ListenAddress() string {
	return net.JoinHostPort(c.APIHost, strconv.Itoa(c.APIPort))
}

func Load() (Config, error) {
	var errs []error
	floatValue := func(name string, fallback, minimum, maximum float64) float64 {
		value, err := envFloat(name, fallback)
		if err != nil || value < minimum || value > maximum {
			if err == nil {
				err = fmt.Errorf("must be between %v and %v", minimum, maximum)
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return fallback
		}
		return value
	}
	intValue := func(name string, fallback, minimum, maximum int) int {
		value, err := envInt(name, fallback)
		if err != nil || value < minimum || value > maximum {
			if err == nil {
				err = fmt.Errorf("must be between %d and %d", minimum, maximum)
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return fallback
		}
		return value
	}

	port := intValue("MARKET_SHARE_API_PORT", 8000, 1, 65535)
	if value := strings.TrimSpace(os.Getenv("MARKET_SHARE_ACTIVE_GLOBAL_WS_EXCHANGES")); value != "" {
		errs = append(errs, errors.New("MARKET_SHARE_ACTIVE_GLOBAL_WS_EXCHANGES: retired; global WebSocket collection is policy-disabled and Binance uses the official 24-hour ticker"))
	}
	level, err := logLevel(env("MARKET_SHARE_LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, err)
	}
	storageMode := strings.ToLower(strings.TrimSpace(env("MARKET_SHARE_STORAGE_MODE", StoragePostgres)))
	if storageMode != StoragePostgres && storageMode != StorageSQLiteMemory {
		errs = append(errs, errors.New("MARKET_SHARE_STORAGE_MODE: must be postgres or sqlite-memory"))
	}
	databaseURL := os.Getenv("MARKET_SHARE_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = env("MARKET_SHARE_DB", DefaultDatabaseURL)
	}
	if storageMode == StoragePostgres && !strings.HasPrefix(strings.ToLower(databaseURL), "postgres://") &&
		!strings.HasPrefix(strings.ToLower(databaseURL), "postgresql://") {
		errs = append(errs, errors.New("MARKET_SHARE_DATABASE_URL: Go backend requires PostgreSQL"))
	}
	contractIdentities, err := loadContractIdentities(os.Getenv("MARKET_SHARE_CONTRACT_IDENTITIES_FILE"))
	if err != nil {
		errs = append(errs, err)
	}
	noticeEnabled, err := envBool("MARKET_SHARE_NOTICE_ENABLED", false)
	if err != nil {
		errs = append(errs, err)
	} else if noticeEnabled {
		errs = append(errs, errors.New("MARKET_SHARE_NOTICE_ENABLED: retired; the in-process Go notice crawler is disabled, use Hermes ingest"))
	}
	noticeSources, err := noticeSourceNames(os.Getenv("MARKET_SHARE_NOTICE_SOURCES"))
	if err != nil {
		errs = append(errs, err)
	} else if len(noticeSources) > 0 {
		errs = append(errs, errors.New("MARKET_SHARE_NOTICE_SOURCES: retired with the in-process Go notice crawler"))
	}
	hermesNoticeIngestToken := strings.TrimSpace(os.Getenv("MARKET_SHARE_HERMES_NOTICE_INGEST_TOKEN"))
	noticePipelineV2Enabled, err := envBool("MARKET_SHARE_NOTICE_PIPELINE_V2_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	if hermesNoticeIngestToken != "" && len(hermesNoticeIngestToken) < 32 {
		errs = append(errs, errors.New("MARKET_SHARE_HERMES_NOTICE_INGEST_TOKEN: must be at least 32 bytes when configured"))
	}
	hermesNoticeAutostart, err := envBool("MARKET_SHARE_HERMES_NOTICE_AUTOSTART", true)
	if err != nil {
		errs = append(errs, err)
	}
	if (hermesNoticeAutostart || noticePipelineV2Enabled) && hermesNoticeIngestToken == "" {
		generated, generateErr := randomIngestToken()
		if generateErr != nil {
			errs = append(errs, fmt.Errorf("generate Hermes notice ingest token: %w", generateErr))
		} else {
			hermesNoticeIngestToken = generated
		}
	}
	llmEnabled, err := envBool("MARKET_SHARE_LLM_ENABLED", true)
	if err != nil {
		errs = append(errs, err)
	}
	questionLearningEnabled, err := envBool("MARKET_SHARE_QUESTION_LEARNING_ENABLED", false)
	if err != nil {
		errs = append(errs, err)
	}
	questionACEnabled, err := envBool("MARKET_SHARE_QUESTION_AC_ENABLED", true)
	if err != nil {
		errs = append(errs, err)
	}
	questionCaptureInput, err := envBool("MARKET_SHARE_DEV_QUESTION_CAPTURE_INPUT", false)
	if err != nil {
		errs = append(errs, err)
	}
	if !llmEnabled {
		hermesNoticeAutostart = false
		if !noticePipelineV2Enabled {
			hermesNoticeIngestToken = ""
		}
		questionLearningEnabled = false
		questionACEnabled = false
	}
	if questionLearningEnabled && storageMode != StoragePostgres {
		errs = append(errs, errors.New("MARKET_SHARE_QUESTION_LEARNING_ENABLED: requires PostgreSQL storage"))
	}
	if noticePipelineV2Enabled && storageMode != StoragePostgres {
		errs = append(errs, errors.New("MARKET_SHARE_NOTICE_PIPELINE_V2_ENABLED: requires PostgreSQL storage"))
	}
	deploymentMode := strings.ToLower(strings.TrimSpace(env("MARKET_SHARE_DEPLOYMENT_MODE", DeploymentStandalone)))
	if deploymentMode != DeploymentStandalone && deploymentMode != DeploymentActive && deploymentMode != DeploymentStandby && deploymentMode != DeploymentManaged {
		errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_MODE: must be standalone, active, standby, or managed"))
	}
	deploymentInstance := strings.TrimSpace(env("MARKET_SHARE_DEPLOYMENT_INSTANCE", DeploymentStandalone))
	deploymentControlToken := strings.TrimSpace(os.Getenv("MARKET_SHARE_DEPLOYMENT_CONTROL_TOKEN"))
	deploymentActiveFile := strings.TrimSpace(os.Getenv("MARKET_SHARE_DEPLOYMENT_ACTIVE_FILE"))
	deploymentInitialActive := strings.ToLower(strings.TrimSpace(env("MARKET_SHARE_DEPLOYMENT_INITIAL_ACTIVE", "blue")))
	if deploymentMode != DeploymentStandalone {
		if storageMode != StoragePostgres {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_MODE: active and standby require PostgreSQL storage"))
		}
		if deploymentInstance == "" || deploymentInstance == DeploymentStandalone {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_INSTANCE: a unique instance name is required for active or standby mode"))
		}
		if len(deploymentControlToken) < 32 {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_CONTROL_TOKEN: must be at least 32 bytes for managed, active, or standby mode"))
		}
		if deploymentControlToken != "" && deploymentControlToken == hermesNoticeIngestToken {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_CONTROL_TOKEN: must differ from MARKET_SHARE_HERMES_NOTICE_INGEST_TOKEN"))
		}
		if deploymentMode == DeploymentManaged && deploymentActiveFile == "" {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_ACTIVE_FILE: is required for managed mode"))
		}
		if deploymentMode == DeploymentManaged && deploymentInstance != "blue" && deploymentInstance != "green" {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_INSTANCE: managed mode requires blue or green"))
		}
		if deploymentInitialActive != "blue" && deploymentInitialActive != "green" {
			errs = append(errs, errors.New("MARKET_SHARE_DEPLOYMENT_INITIAL_ACTIVE: must be blue or green"))
		}
	}

	config := Config{
		DatabaseURL:                     databaseURL,
		StorageMode:                     storageMode,
		AggregationEngine:               strings.ToLower(strings.TrimSpace(env("MARKET_SHARE_AGGREGATION_ENGINE", "go-oracle"))),
		APIHost:                         env("MARKET_SHARE_API_HOST", "127.0.0.1"),
		APIPort:                         port,
		LogLevel:                        level,
		RegistryRefresh:                 seconds(floatValue("MARKET_SHARE_REGISTRY_REFRESH_SEC", 1800, minimumRegistryRefreshSec, maximumRegistryRefreshSec)),
		BinanceTickerRefresh:            seconds(floatValue("MARKET_SHARE_BINANCE_TICKER_REFRESH_SEC", 60, 10, maximumRefreshSeconds)),
		NoticeEnabled:                   false,
		NoticeSources:                   nil,
		NoticePoll:                      seconds(floatValue("MARKET_SHARE_NOTICE_POLL_SEC", 600, 1, maximumRefreshSeconds)),
		HermesNoticeIngestToken:         hermesNoticeIngestToken,
		NoticePipelineV2Enabled:         noticePipelineV2Enabled,
		HermesNoticeAutostart:           hermesNoticeAutostart,
		HermesNoticeWorkerBin:           strings.TrimSpace(os.Getenv("MARKET_SHARE_HERMES_NOTICE_WORKER_BIN")),
		HermesNoticeBin:                 env("HERMES_BIN", "hermes"),
		VisionOCRBin:                    strings.TrimSpace(os.Getenv("MARKET_SHARE_VISION_OCR_BIN")),
		HermesNoticeOutputDir:           env("MARKET_SHARE_HERMES_NOTICE_OUTPUT_DIR", ".tmp/hermes-notices"),
		LLMEnabled:                      llmEnabled,
		QuestionLearningEnabled:         questionLearningEnabled,
		QuestionACEnabled:               questionACEnabled,
		QuestionEvidenceRetentionDays:   intValue("MARKET_SHARE_QUESTION_EVIDENCE_RETENTION_DAYS", 0, 0, 90),
		DevelopmentQuestionTimeout:      time.Duration(intValue("MARKET_SHARE_DEV_QUESTION_TIMEOUT_SEC", 300, 1, 300)) * time.Second,
		DevelopmentQuestionCaptureInput: questionCaptureInput,
		HermesNoticeMaxAnalysesPerExchangePerRun: intValue(
			"MARKET_SHARE_HERMES_MAX_ANALYSES_PER_EXCHANGE_PER_RUN", 1, 1, 20,
		),
		QueueSize:               intValue("MARKET_SHARE_QUEUE_SIZE", 32768, 1, maximumQueueSize),
		FXMaxAge:                time.Duration(intValue("MARKET_SHARE_FX_MAX_AGE_MS", 120000, 1, maximumFXAgeMilliseconds)) * time.Millisecond,
		ReferenceOrderKRW:       floatValue("MARKET_SHARE_REFERENCE_ORDER_KRW", 10000000, 1, maximumReferenceOrderKRW),
		OrderBookStale:          seconds(floatValue("MARKET_SHARE_ORDERBOOK_STALE_SEC", 30, 1, maximumOrderBookStaleSec)),
		PriceGapQuarantineBPS:   floatValue("MARKET_SHARE_PRICE_GAP_QUARANTINE_BPS", 2000, 0, maximumPriceGapQuarantine),
		ContractIdentities:      contractIdentities,
		ShutdownTimeout:         seconds(floatValue("MARKET_SHARE_SHUTDOWN_TIMEOUT_SEC", 15, 1, maximumShutdownSeconds)),
		MemoryLimitMiB:          intValue("MARKET_SHARE_MEMORY_LIMIT_MIB", 768, 128, 4096),
		DeploymentMode:          deploymentMode,
		DeploymentInstance:      deploymentInstance,
		DeploymentControlToken:  deploymentControlToken,
		DeploymentBufferMiB:     intValue("MARKET_SHARE_DEPLOYMENT_BUFFER_MIB", 128, 16, 512),
		DeploymentActiveFile:    deploymentActiveFile,
		DeploymentInitialActive: deploymentInitialActive,
	}
	if strings.TrimSpace(config.APIHost) == "" {
		errs = append(errs, errors.New("MARKET_SHARE_API_HOST: cannot be empty"))
	}
	if config.AggregationEngine != "go-oracle" && config.AggregationEngine != "rust-ffi" {
		errs = append(errs, errors.New("MARKET_SHARE_AGGREGATION_ENGINE: must be go-oracle or rust-ffi"))
	}
	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return config, nil
}

func randomIngestToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func loadContractIdentities(path string) (map[string]marketanalysis.ContractIdentityProof, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("MARKET_SHARE_CONTRACT_IDENTITIES_FILE: %w", err)
	}
	proofs := map[string]marketanalysis.ContractIdentityProof{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proofs); err != nil {
		return nil, fmt.Errorf("MARKET_SHARE_CONTRACT_IDENTITIES_FILE: decode: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("MARKET_SHARE_CONTRACT_IDENTITIES_FILE: decode: trailing JSON value")
	}
	if len(proofs) == 0 {
		return nil, errors.New("MARKET_SHARE_CONTRACT_IDENTITIES_FILE: at least one asset proof is required")
	}
	for asset, proof := range proofs {
		normalized := strings.ToUpper(strings.TrimSpace(asset))
		if asset != normalized {
			return nil, fmt.Errorf("MARKET_SHARE_CONTRACT_IDENTITIES_FILE: asset key %q is not normalized", asset)
		}
		if err := proof.Validate(asset); err != nil {
			return nil, fmt.Errorf("MARKET_SHARE_CONTRACT_IDENTITIES_FILE: %s: %w", asset, err)
		}
	}
	return proofs, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envFloat(name string, fallback float64) (float64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback, fmt.Errorf("must be a finite number")
	}
	return value, nil
}

func envInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("must be an integer")
	}
	return value, nil
}

func envBool(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s: must be true or false", name)
	}
	return value, nil
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

func noticeSourceNames(raw string) ([]string, error) {
	allowed := map[string]struct{}{
		"upbit": {}, "bithumb": {}, "coinone": {}, "korbit": {},
		"gopax": {}, "binance": {}, "bybit": {}, "okx": {},
	}
	seen := make(map[string]struct{})
	result := []string{}
	for _, item := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("MARKET_SHARE_NOTICE_SOURCES: unsupported source %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func logLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("MARKET_SHARE_LOG_LEVEL: unsupported level %q", raw)
	}
}

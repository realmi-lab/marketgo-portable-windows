// Package serviceconfig owns explicit startup settings for the separate
// MarketGo processes. It never connects to a database or starts a service.
package serviceconfig

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Role string

const (
	Collector  Role = "collector"
	Calculator Role = "calculator"
	Gateway    Role = "gateway"
	NoticeAI   Role = "notice-ai"
	prefix          = "MARKET_SHARE_SERVICES_"
)

type Config struct {
	Role                Role
	Instance            string
	DatabaseURL         string
	ServiceToken        string
	ListenAddress       string
	PublicListenAddress string
	CollectorURL        string
	ComputeURL          string
	IntelligenceURL     string
	StateDir            string
	HermesHome          string
	NoticeIngestToken   string
}

var instancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func Load(role Role) (Config, error) { return load(role, os.Getenv) }

func load(role Role, getenv func(string) string) (Config, error) {
	c := Config{Role: role}
	read := func(name string) string { return strings.TrimSpace(getenv(prefix + name)) }
	switch role {
	case Collector, Calculator, Gateway, NoticeAI:
	default:
		return c, errors.New("select one service: collector, calculator, gateway, or notice-ai")
	}
	c.Instance = read("INSTANCE")
	if !instancePattern.MatchString(c.Instance) {
		return c, errors.New(prefix + "INSTANCE must be an explicit 1-64 character instance name (letters, digits, dot, underscore, or hyphen)")
	}
	if role != Gateway {
		c.DatabaseURL = read("DATABASE_URL")
		if err := ValidateDatabaseURL(c.DatabaseURL); err != nil {
			return c, fmt.Errorf("%sDATABASE_URL: %w", prefix, err)
		}
	}
	if role == Collector {
		c.ServiceToken = read("SERVICE_TOKEN")
		if c.ServiceToken != "" {
			if err := validateToken(c.ServiceToken); err != nil {
				return c, fmt.Errorf("invalid collector service token: %w", err)
			}
		}
		allowContainer := false
		if value := read("COLLECTOR_ALLOW_CONTAINER_LISTEN"); value != "" {
			var err error
			allowContainer, err = strconv.ParseBool(value)
			if err != nil {
				return c, errors.New(prefix + "COLLECTOR_ALLOW_CONTAINER_LISTEN must be a boolean")
			}
		}
		c.ListenAddress = read("COLLECTOR_LISTEN")
		if c.ListenAddress == "" {
			c.ListenAddress = "127.0.0.1:28001"
		}
		if err := validateListen(c.ListenAddress); err != nil {
			return c, fmt.Errorf("%sCOLLECTOR_LISTEN: %w", prefix, err)
		}
		host, _, _ := net.SplitHostPort(c.ListenAddress)
		if ip := net.ParseIP(host); ip == nil || !(ip.IsLoopback() || (allowContainer && ip.IsUnspecified())) {
			return c, errors.New(prefix + "COLLECTOR_LISTEN requires a loopback IP, or an explicitly allowed container wildcard IP")
		}
		return c, nil
	}
	c.ServiceToken = read("SERVICE_TOKEN")
	if err := validateToken(c.ServiceToken); err != nil {
		return c, fmt.Errorf("%sSERVICE_TOKEN: %w", prefix, err)
	}
	listenName := map[Role]string{Calculator: "COMPUTE_LISTEN", Gateway: "API_LISTEN", NoticeAI: "INTELLIGENCE_LISTEN"}[role]
	c.ListenAddress = read(listenName)
	if err := validateListen(c.ListenAddress); err != nil {
		return c, fmt.Errorf("%s%s: %w", prefix, listenName, err)
	}
	if role == Calculator {
		c.PublicListenAddress = read("API_LISTEN")
		if c.PublicListenAddress == "" {
			c.PublicListenAddress = "127.0.0.1:28000"
		}
		if err := validateListen(c.PublicListenAddress); err != nil {
			return c, fmt.Errorf("invalid calculator public listen: %w", err)
		}
		if c.PublicListenAddress == c.ListenAddress {
			return c, errors.New("calculator public and private listeners must differ")
		}
		c.IntelligenceURL = read("INTELLIGENCE_URL")
		if c.IntelligenceURL == "" {
			c.IntelligenceURL = "http://127.0.0.1:28003"
		}
		if err := validateOrigin(c.IntelligenceURL); err != nil {
			return c, fmt.Errorf("invalid intelligence URL: %w", err)
		}
		c.CollectorURL = read("COLLECTOR_URL")
		if c.CollectorURL != "" {
			if err := validateOrigin(c.CollectorURL); err != nil {
				return c, fmt.Errorf("invalid collector URL: %w", err)
			}
		}
	}
	if role == Gateway || role == NoticeAI {
		c.ComputeURL = read("COMPUTE_URL")
		endpoints := []struct{ name, value string }{{"COMPUTE_URL", c.ComputeURL}}
		if role == Gateway {
			c.IntelligenceURL = read("INTELLIGENCE_URL")
			endpoints = append(endpoints, struct{ name, value string }{"INTELLIGENCE_URL", c.IntelligenceURL})
		}
		for _, endpoint := range endpoints {
			if err := validateOrigin(endpoint.value); err != nil {
				return c, fmt.Errorf("%s%s: %w", prefix, endpoint.name, err)
			}
		}
	}
	if role == NoticeAI {
		c.StateDir = read("STATE_DIR")
		if err := ValidateStateDir(c.StateDir); err != nil {
			return c, fmt.Errorf("%sSTATE_DIR: %w", prefix, err)
		}
		c.StateDir = filepath.Clean(c.StateDir)
		c.HermesHome = read("HERMES_HOME")
		if c.HermesHome == "" {
			c.HermesHome = filepath.Join(c.StateDir, "hermes")
		}
		if err := ValidateStateDir(c.HermesHome); err != nil {
			return c, fmt.Errorf("%sHERMES_HOME: %w", prefix, err)
		}
		c.HermesHome = filepath.Clean(c.HermesHome)
		c.NoticeIngestToken = read("NOTICE_INGEST_TOKEN")
		if err := validateToken(c.NoticeIngestToken); err != nil {
			return c, fmt.Errorf("%sNOTICE_INGEST_TOKEN: %w", prefix, err)
		}
		if c.NoticeIngestToken == c.ServiceToken {
			return c, errors.New("service RPC and notice ingest tokens must differ")
		}
	}
	return c, nil
}

// ValidateDatabaseURL permits an explicitly chosen existing MarketGo database;
// project-name prefixes are not a database identity or authorization boundary.
// Errors deliberately omit the URL because it can contain credentials.
func ValidateDatabaseURL(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Fragment != "" || strings.TrimSpace(dsn) != dsn {
		return errors.New("an explicit PostgreSQL URL with host and database name is required")
	}
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" || database == "." || database == ".." || strings.ContainsAny(database, "/\x00\r\n\t") || strings.TrimSpace(database) != database {
		return errors.New("the PostgreSQL URL must name exactly one database")
	}
	if u.Port() != "" {
		if err := validatePort(u.Port()); err != nil {
			return err
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("invalid PostgreSQL URL query")
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "dbname", "database", "host", "hostaddr", "port", "service", "servicefile":
			return errors.New("PostgreSQL URL query must not override connection identity")
		}
	}
	return nil
}

func ValidateStateDir(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Dir(filepath.Clean(dir)) == filepath.Clean(dir) || strings.TrimSpace(dir) != dir {
		return errors.New("an explicit absolute service state directory below the filesystem root is required")
	}
	return nil
}

func validateToken(token string) error {
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("a private token of at least 32 bytes without whitespace is required")
	}
	return nil
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("port must be a number from 1 to 65535")
	}
	return nil
}

func validateListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" || strings.ContainsAny(host, " /\t\r\n") {
		return errors.New("an explicit host:port listen address is required")
	}
	return validatePort(port)
}

func validateOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("an HTTP(S) origin without credentials, path, query, or fragment is required")
	}
	if u.Port() != "" {
		return validatePort(u.Port())
	}
	return nil
}

// RuntimeEnvironment bridges only the owned settings into the existing app
// loader. Call ApplyRuntimeEnvironment once before config.Load at process
// startup. Inherited monolith database/deployment/autostart settings cannot
// select a different writer role or start a notice worker in these processes.
func (c Config) RuntimeEnvironment() map[string]string {
	if c.Role != Calculator && c.Role != NoticeAI {
		return nil
	}
	env := map[string]string{
		"MARKET_SHARE_DATABASE_URL":               c.DatabaseURL,
		"MARKET_SHARE_STORAGE_MODE":               "postgres",
		"MARKET_SHARE_DEPLOYMENT_MODE":            "standalone",
		"MARKET_SHARE_DEPLOYMENT_INSTANCE":        c.Instance,
		"MARKET_SHARE_HERMES_NOTICE_AUTOSTART":    "false",
		"MARKET_SHARE_HERMES_NOTICE_INGEST_TOKEN": c.NoticeIngestToken,
	}
	if c.Role == Calculator {
		env["MARKET_SHARE_LLM_ENABLED"] = "false"
		env["MARKET_SHARE_QUESTION_LEARNING_ENABLED"] = "false"
		env["MARKET_SHARE_NOTICE_PIPELINE_V2_ENABLED"] = "false"
	} else {
		env["HERMES_HOME"] = c.HermesHome
		env["MARKET_SHARE_HERMES_NOTICE_OUTPUT_DIR"] = filepath.Join(c.StateDir, "notice-ai")
		env["MARKET_SHARE_HERMES_PRIORITY_DIR"] = filepath.Join(c.StateDir, "hermes-priority")
	}
	return env
}

func (c Config) ApplyRuntimeEnvironment() error {
	for name, value := range c.RuntimeEnvironment() {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set service runtime variable %s: %w", name, err)
		}
	}
	return nil
}

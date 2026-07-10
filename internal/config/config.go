// Package config loads and validates process configuration.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListen          = "127.0.0.1:8080"
	defaultReadTimeout     = 15 * time.Second
	defaultWriteTimeout    = 60 * time.Second
	defaultShutdownTimeout = 15 * time.Second
	defaultMaxRequestBytes = int64(1 << 20)
	defaultJWTClockSkew    = 30 * time.Second
)

// Config is the validated process configuration.
type Config struct {
	Server         ServerConfig
	Database       DatabaseConfig
	Logging        LoggingConfig
	Authentication AuthenticationConfig
}

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	Listen          string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	MaxRequestBytes int64
}

// DatabaseConfig controls PostgreSQL connectivity.
type DatabaseConfig struct {
	Required bool
	DSN      string
}

// LoggingConfig controls structured logging.
type LoggingConfig struct {
	Level  string
	Format string
}

// AuthenticationConfig controls upstream JWT verification.
type AuthenticationConfig struct {
	Enabled           bool
	Issuer            string
	Audience          string
	PublicKeyFile     string
	KeyID             string
	ClockSkew         time.Duration
	UsernameClaim     string
	ServiceActorClaim string
}

// LookupEnv matches os.LookupEnv and makes environment overrides testable.
type LookupEnv func(string) (string, bool)

// Default returns safe development defaults.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Listen:          defaultListen,
			ReadTimeout:     defaultReadTimeout,
			WriteTimeout:    defaultWriteTimeout,
			ShutdownTimeout: defaultShutdownTimeout,
			MaxRequestBytes: defaultMaxRequestBytes,
		},
		Database: DatabaseConfig{},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Authentication: AuthenticationConfig{
			ClockSkew:         defaultJWTClockSkew,
			UsernameClaim:     "preferred_username",
			ServiceActorClaim: "azp",
		},
	}
}

// Load reads the documented scalar YAML subset, applies environment overrides,
// and validates the result. The bootstrap intentionally avoids third-party
// dependencies; replacing this parser with a full YAML library is allowed only
// as a dedicated, tested change.
func Load(path string, lookup LookupEnv) (Config, error) {
	cfg := Default()
	if lookup == nil {
		lookup = os.LookupEnv
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		values, err := parseScalarYAML(string(data))
		if err != nil {
			return Config{}, err
		}
		if err := applyFileValues(&cfg, values); err != nil {
			return Config{}, err
		}
	}

	if err := applyEnvironment(&cfg, lookup); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects unsafe or unusable settings.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Server.Listen) == "" {
		return errors.New("server.listen must not be empty")
	}
	if c.Server.ReadTimeout <= 0 {
		return errors.New("server.read_timeout must be positive")
	}
	if c.Server.WriteTimeout <= 0 {
		return errors.New("server.write_timeout must be positive")
	}
	if c.Server.ShutdownTimeout <= 0 {
		return errors.New("server.shutdown_timeout must be positive")
	}
	if c.Server.MaxRequestBytes <= 0 {
		return errors.New("server.max_request_bytes must be positive")
	}
	if c.Database.Required && strings.TrimSpace(c.Database.DSN) == "" {
		return errors.New("database.dsn is required when database.required is true")
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported logging.level %q", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("unsupported logging.format %q", c.Logging.Format)
	}
	if c.Authentication.ClockSkew < 0 || c.Authentication.ClockSkew > 5*time.Minute {
		return errors.New("authentication.clock_skew must be between 0 and 5 minutes")
	}
	if strings.ContainsAny(c.Authentication.UsernameClaim, " \t\r\n") {
		return errors.New("authentication.username_claim must not contain whitespace")
	}
	if strings.ContainsAny(c.Authentication.ServiceActorClaim, " \t\r\n") {
		return errors.New("authentication.service_actor_claim must not contain whitespace")
	}
	if c.Authentication.Enabled {
		if strings.TrimSpace(c.Authentication.Issuer) == "" {
			return errors.New("authentication.issuer is required when authentication is enabled")
		}
		if strings.TrimSpace(c.Authentication.Audience) == "" {
			return errors.New("authentication.audience is required when authentication is enabled")
		}
		if strings.TrimSpace(c.Authentication.PublicKeyFile) == "" {
			return errors.New("authentication.public_key_file is required when authentication is enabled")
		}
		if strings.TrimSpace(c.Database.DSN) == "" {
			return errors.New("database.dsn is required when authentication is enabled")
		}
	}
	return nil
}

func parseScalarYAML(input string) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(input))
	section := ""
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		raw := strings.TrimRight(scanner.Text(), " \t\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if strings.HasSuffix(trimmed, ":") && !strings.Contains(strings.TrimSuffix(trimmed, ":"), ":") {
			if indent != 0 {
				return nil, fmt.Errorf("config line %d: sections must be top-level", lineNo)
			}
			section = strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			if section == "" {
				return nil, fmt.Errorf("config line %d: empty section", lineNo)
			}
			continue
		}

		if section == "" || indent == 0 {
			return nil, fmt.Errorf("config line %d: expected an indented key under a section", lineNo)
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("config line %d: expected key: value", lineNo)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		fullKey := section + "." + strings.TrimSpace(key)
		if _, exists := values[fullKey]; exists {
			return nil, fmt.Errorf("config line %d: duplicate key %s", lineNo, fullKey)
		}
		values[fullKey] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan config: %w", err)
	}
	return values, nil
}

func applyFileValues(cfg *Config, values map[string]string) error {
	for key, value := range values {
		switch key {
		case "server.listen":
			cfg.Server.Listen = value
		case "server.read_timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Server.ReadTimeout = d
		case "server.write_timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Server.WriteTimeout = d
		case "server.shutdown_timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Server.ShutdownTimeout = d
		case "server.max_request_bytes":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Server.MaxRequestBytes = n
		case "database.required":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Database.Required = b
		case "database.dsn":
			cfg.Database.DSN = value
		case "logging.level":
			cfg.Logging.Level = strings.ToLower(value)
		case "logging.format":
			cfg.Logging.Format = strings.ToLower(value)
		case "authentication.enabled":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Authentication.Enabled = b
		case "authentication.issuer":
			cfg.Authentication.Issuer = value
		case "authentication.audience":
			cfg.Authentication.Audience = value
		case "authentication.public_key_file":
			cfg.Authentication.PublicKeyFile = value
		case "authentication.key_id":
			cfg.Authentication.KeyID = value
		case "authentication.clock_skew":
			d, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Authentication.ClockSkew = d
		case "authentication.username_claim":
			cfg.Authentication.UsernameClaim = value
		case "authentication.service_actor_claim":
			cfg.Authentication.ServiceActorClaim = value
		default:
			return fmt.Errorf("unknown config key %q", key)
		}
	}
	return nil
}

func applyEnvironment(cfg *Config, lookup LookupEnv) error {
	if value, ok := lookup("SWITCH_MANAGER_SERVER_LISTEN"); ok {
		cfg.Server.Listen = value
	}
	if value, ok := lookup("SWITCH_MANAGER_DATABASE_DSN"); ok {
		cfg.Database.DSN = value
	}
	if value, ok := lookup("SWITCH_MANAGER_DATABASE_REQUIRED"); ok {
		required, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse SWITCH_MANAGER_DATABASE_REQUIRED: %w", err)
		}
		cfg.Database.Required = required
	}
	if value, ok := lookup("SWITCH_MANAGER_LOG_LEVEL"); ok {
		cfg.Logging.Level = strings.ToLower(value)
	}
	if value, ok := lookup("SWITCH_MANAGER_LOG_FORMAT"); ok {
		cfg.Logging.Format = strings.ToLower(value)
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse SWITCH_MANAGER_AUTH_ENABLED: %w", err)
		}
		cfg.Authentication.Enabled = enabled
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_ISSUER"); ok {
		cfg.Authentication.Issuer = value
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_AUDIENCE"); ok {
		cfg.Authentication.Audience = value
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_PUBLIC_KEY_FILE"); ok {
		cfg.Authentication.PublicKeyFile = value
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_KEY_ID"); ok {
		cfg.Authentication.KeyID = value
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_CLOCK_SKEW"); ok {
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse SWITCH_MANAGER_AUTH_CLOCK_SKEW: %w", err)
		}
		cfg.Authentication.ClockSkew = d
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_USERNAME_CLAIM"); ok {
		cfg.Authentication.UsernameClaim = value
	}
	if value, ok := lookup("SWITCH_MANAGER_AUTH_SERVICE_ACTOR_CLAIM"); ok {
		cfg.Authentication.ServiceActorClaim = value
	}
	return nil
}

package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	AppName             string
	BaseURL             string
	PlainHTTPAddr       string
	HTTPAddr            string
	DataDir             string
	DatabasePath        string
	LocalStorageDir     string
	StorageBackend      string
	CertFile            string
	KeyFile             string
	RoomTTL             time.Duration
	MaxUploadSize       int64
	DefaultChunkSize    int64
	WebsocketBufferSize int
	LogLevel            slog.Level
	WebRTC              WebRTCConfig
	S3                  S3Config
}

type WebRTCConfig struct {
	Enabled       bool
	STUNServers   []string
	TURNServers   []string
	RelayFallback string
}

type S3Config struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

func Load() Config {
	dataDir := env("GOSSH_DATA_DIR", filepath.Join(".", "data"))

	return Config{
		AppName:             env("GOSSH_APP_NAME", "gossh"),
		BaseURL:             env("GOSSH_BASE_URL", "https://127.0.0.1:8443"),
		PlainHTTPAddr:       env("GOSSH_PLAIN_ADDR", ":8080"),
		HTTPAddr:            env("GOSSH_ADDR", ":8443"),
		DataDir:             dataDir,
		DatabasePath:        env("GOSSH_DB_PATH", filepath.Join(dataDir, "gossh.db")),
		LocalStorageDir:     env("GOSSH_LOCAL_STORAGE_DIR", filepath.Join(dataDir, "objects")),
		StorageBackend:      env("GOSSH_STORAGE_BACKEND", "local"),
		CertFile:            env("GOSSH_TLS_CERT", filepath.Join(dataDir, "certs", "cert.pem")),
		KeyFile:             env("GOSSH_TLS_KEY", filepath.Join(dataDir, "certs", "key.pem")),
		RoomTTL:             envDuration("GOSSH_ROOM_TTL", 24*time.Hour),
		MaxUploadSize:       envInt64("GOSSH_MAX_UPLOAD_SIZE", 20<<30),
		DefaultChunkSize:    envInt64("GOSSH_DEFAULT_CHUNK_SIZE", 512<<10),
		WebsocketBufferSize: envInt("GOSSH_WS_BUFFER_SIZE", 1024),
		LogLevel:            envLevel("GOSSH_LOG_LEVEL", slog.LevelInfo),
		WebRTC: WebRTCConfig{
			Enabled:       envBool("GOSSH_WEBRTC_ENABLED", false),
			STUNServers:   splitCSV(env("GOSSH_STUN_SERVERS", "")),
			TURNServers:   splitCSV(env("GOSSH_TURN_SERVERS", "")),
			RelayFallback: env("GOSSH_WEBRTC_FALLBACK", "quic-relay"),
		},
		S3: S3Config{
			Endpoint:  env("GOSSH_S3_ENDPOINT", ""),
			Bucket:    env("GOSSH_S3_BUCKET", ""),
			Region:    env("GOSSH_S3_REGION", "auto"),
			AccessKey: env("GOSSH_S3_ACCESS_KEY", ""),
			SecretKey: env("GOSSH_S3_SECRET_KEY", ""),
			UseSSL:    envBool("GOSSH_S3_USE_SSL", true),
		},
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		parsed, err := time.ParseDuration(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func envLevel(key string, fallback slog.Level) slog.Level {
	switch env(key, "") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return fallback
	}
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := make([]string, 0, 4)
	for _, item := range filepath.SplitList(value) {
		if item != "" {
			parts = append(parts, item)
		}
	}
	if len(parts) > 0 {
		return parts
	}
	raw := []string{}
	current := ""
	for _, ch := range value {
		if ch == ',' {
			if current != "" {
				raw = append(raw, current)
				current = ""
			}
			continue
		}
		current += string(ch)
	}
	if current != "" {
		raw = append(raw, current)
	}
	return raw
}

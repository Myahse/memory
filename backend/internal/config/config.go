// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Plan struct {
	StorageBytes   int64
	AIQueries      int // per month; <=0 means unlimited
	ProcessedItems int // per month; <=0 means unlimited
	MaxFileBytes   int64
}

type Config struct {
	Addr        string
	Mode        string // api | worker | all
	DatabaseURL string
	CORSOrigins []string
	PublicURL   string // base URL of the web app, used for share links

	SupabaseURL        string
	SupabaseServiceKey string
	SupabaseJWTSecret  string // legacy HS256 secret; when empty, JWKS is used
	StorageBucket      string

	// AI provider selection. Each capability can use a different provider.
	LLMProvider       string // anthropic | openai | mock
	VisionProvider    string // anthropic | openai | mock
	SpeechProvider    string // openai | mock
	EmbeddingProvider string // openai | mock

	AnthropicAPIKey  string
	AnthropicModel   string
	OpenAIAPIKey     string
	OpenAIBaseURL    string
	OpenAIChatModel  string
	OpenAISTTModel   string
	OpenAIEmbedModel string

	AITimeout time.Duration

	WorkerConcurrency int
	WorkerPoll        time.Duration
	MaxJobAttempts    int

	Free Plan
	Pro  Plan
}

func Load() (*Config, error) {
	c := &Config{
		Addr:        env("ADDR", ":8080"),
		Mode:        env("MODE", "all"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		CORSOrigins: splitList(env("CORS_ORIGINS", "http://localhost:5173")),
		PublicURL:   strings.TrimRight(env("PUBLIC_URL", "http://localhost:5173"), "/"),

		SupabaseURL:        strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseServiceKey: os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseJWTSecret:  os.Getenv("SUPABASE_JWT_SECRET"),
		StorageBucket:      env("STORAGE_BUCKET", "memories"),

		LLMProvider:       env("AI_LLM_PROVIDER", "anthropic"),
		VisionProvider:    env("AI_VISION_PROVIDER", "anthropic"),
		SpeechProvider:    env("AI_SPEECH_PROVIDER", "openai"),
		EmbeddingProvider: env("AI_EMBEDDING_PROVIDER", "openai"),

		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:   env("ANTHROPIC_MODEL", "claude-opus-5-5"),
		OpenAIAPIKey:     os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:    strings.TrimRight(env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		OpenAIChatModel:  env("OPENAI_CHAT_MODEL", "gpt-4.1-mini"),
		OpenAISTTModel:   env("OPENAI_STT_MODEL", "gpt-4o-transcribe"),
		OpenAIEmbedModel: env("OPENAI_EMBED_MODEL", "text-embedding-3-small"),

		AITimeout: envDuration("AI_TIMEOUT", 90*time.Second),

		WorkerConcurrency: envInt("WORKER_CONCURRENCY", 4),
		WorkerPoll:        envDuration("WORKER_POLL", 2*time.Second),
		MaxJobAttempts:    envInt("MAX_JOB_ATTEMPTS", 3),

		Free: Plan{
			StorageBytes:   envInt64("FREE_STORAGE_BYTES", 1<<30), // 1 GiB
			AIQueries:      envInt("FREE_AI_QUERIES", 50),
			ProcessedItems: envInt("FREE_PROCESSED_ITEMS", 200),
			MaxFileBytes:   envInt64("FREE_MAX_FILE_BYTES", 25<<20), // 25 MiB
		},
		Pro: Plan{
			StorageBytes:   envInt64("PRO_STORAGE_BYTES", 50<<30), // 50 GiB
			AIQueries:      envInt("PRO_AI_QUERIES", 0),
			ProcessedItems: envInt("PRO_PROCESSED_ITEMS", 0),
			MaxFileBytes:   envInt64("PRO_MAX_FILE_BYTES", 100<<20), // 100 MiB
		},
	}

	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.SupabaseURL == "" {
		missing = append(missing, "SUPABASE_URL")
	}
	if c.SupabaseServiceKey == "" {
		missing = append(missing, "SUPABASE_SERVICE_ROLE_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	switch c.Mode {
	case "api", "worker", "all":
	default:
		return nil, fmt.Errorf("MODE must be api, worker or all (got %q)", c.Mode)
	}
	return c, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envInt64(k string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(k), 10, 64); err == nil {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

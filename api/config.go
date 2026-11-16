package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is read once from the environment. Limits follow Judge0's units:
// seconds for time, kilobytes for memory and file size.
type Config struct {
	Role         string // "api" or "worker"
	Project      string
	Database     string
	Topic        string // Pub/Sub topic that carries queued groups
	AuthTokens   []string
	RunnerNoAuth bool // local development only

	DefaultCPU, MaxCPU       float64
	MaxWall                  float64
	DefaultMemKB, MaxMemKB   int
	DefaultFileKB, MaxFileKB int
	DefaultProcs, MaxProcs   int

	MaxSourceKB int
	MaxStdinKB  int
	MaxBatch    int
	GroupSize   int // most tests sent to a runner in one request
	TTL         time.Duration

	// Network access for submissions that ask for it with enable_network.
	// Off unless the deployment turns it on.
	AllowNetwork               bool
	DefaultNetTime, MaxNetTime float64 // seconds a run may spend waiting on the network
	MaxDownloadKB              int     // most a run may receive, and the largest file it may write
	NetCPUUsPerMB              int     // CPU time not counted, per MB received
}

// Protocol overhead on top of what a program downloads, as counted on the
// sandbox's network interface: measured at 1 to 7%.
const netOverheadPct = 8

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("%s must be an integer, got %q", k, v)
		}
		return n
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Fatalf("%s must be a number, got %q", k, v)
		}
		return n
	}
	return def
}

func loadConfig() *Config {
	c := &Config{
		Role:         env("ROLE", "api"),
		Project:      env("PROJECT_ID", os.Getenv("GOOGLE_CLOUD_PROJECT")),
		Database:     env("FIRESTORE_DB", "judge"),
		Topic:        env("QUEUE_TOPIC", "judge-runs"),
		RunnerNoAuth: os.Getenv("RUNNER_NO_AUTH") == "1",

		// Codeforces-style defaults: 2 s and 256 MB, never above 2 s / 512 MB.
		DefaultCPU:    envFloat("DEFAULT_CPU_TIME", 2),
		MaxCPU:        envFloat("MAX_CPU_TIME", 2),
		MaxWall:       envFloat("MAX_WALL_TIME", 10),
		DefaultMemKB:  envInt("DEFAULT_MEMORY_KB", 256*1024),
		MaxMemKB:      envInt("MAX_MEMORY_KB", 512*1024),
		DefaultFileKB: envInt("DEFAULT_FILE_KB", 1024),
		MaxFileKB:     envInt("MAX_FILE_KB", 4096),
		DefaultProcs:  envInt("DEFAULT_PROCESSES", 64),
		MaxProcs:      envInt("MAX_PROCESSES", 128),

		MaxSourceKB: envInt("MAX_SOURCE_KB", 256),
		MaxStdinKB:  envInt("MAX_STDIN_KB", 1024),
		MaxBatch:    envInt("MAX_BATCH", 100),
		GroupSize:   envInt("GROUP_SIZE", 25),
		// Six days, not seven: Firestore removes expired documents within
		// about a day, and nothing may be kept beyond seven.
		TTL: time.Duration(envInt("SUBMISSION_TTL_HOURS", 144)) * time.Hour,

		AllowNetwork:   os.Getenv("ALLOW_ENABLE_NETWORK") == "1" || os.Getenv("ALLOW_ENABLE_NETWORK") == "true",
		DefaultNetTime: envFloat("NETWORK_TIME_LIMIT", 30),
		MaxNetTime:     envFloat("MAX_NETWORK_TIME_LIMIT", 60),
		MaxDownloadKB:  envInt("MAX_DOWNLOAD_MB", 125) * 1024,
		// Receiving 1 MB inside a sandbox was measured to cost 6 to 14 ms.
		NetCPUUsPerMB: int(envFloat("NETWORK_CPU_MS_PER_MB", 15) * 1000),
	}
	if c.DefaultNetTime > c.MaxNetTime {
		c.DefaultNetTime = c.MaxNetTime
	}
	if c.AllowNetwork && (c.MaxNetTime <= 0 || c.MaxNetTime > 240 || c.MaxDownloadKB <= 0 || c.MaxDownloadKB > 900*1024 || c.NetCPUUsPerMB < 0) {
		log.Fatal("network limits are out of range: MAX_NETWORK_TIME_LIMIT must be 1 to 240 seconds and MAX_DOWNLOAD_MB 1 to 900")
	}
	for _, t := range strings.Split(os.Getenv("AUTH_TOKENS"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			c.AuthTokens = append(c.AuthTokens, t)
		}
	}
	if c.Project == "" {
		log.Fatal("PROJECT_ID is required")
	}
	if c.Role != "api" && c.Role != "worker" {
		log.Fatalf("ROLE must be api or worker, got %q", c.Role)
	}
	if c.Role == "api" && len(c.AuthTokens) == 0 {
		log.Fatal("AUTH_TOKENS is required: refusing to run an open code-execution API")
	}
	return c
}

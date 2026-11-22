package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds runtime configuration sourced from environment variables and
// projected files.
type Config struct {
	// Port is the HTTP port the backend listens on.
	Port string

	// EverestAPIURL is the base URL of the OpenEverest API (e.g.
	// http://everest.everest-system.svc.cluster.local:8080).
	EverestAPIURL string

	// ServiceUser and ServicePassword are the credentials of the OpenEverest
	// user the daemon logs in as. The host doesn't issue plugin service tokens
	// yet (spec 003 §10.4).
	ServiceUser     string
	ServicePassword string

	// DBDriver is the storage driver: sqlite or postgres.
	DBDriver string

	// DBPath is the on-disk location of the SQLite database.
	DBPath string

	// DBURI is the connection URI of the PostgreSQL database.
	DBURI string

	// Cluster is the OpenEverest cluster whose lifecycle events are recorded.
	Cluster string

	// Namespaces, Kinds and Reasons are optional comma-separated filters passed
	// to the events API. Empty means all.
	Namespaces string
	Kinds      string
	Reasons    string
}

func FromEnv() Config {
	return Config{
		Port:            getenv("PORT", "8080"),
		EverestAPIURL:   discoverEverestAPI(),
		ServiceUser:     os.Getenv("EVEREST_SERVICE_USER"),
		ServicePassword: os.Getenv("EVEREST_SERVICE_PASSWORD"),
		DBDriver:        getenv("AUDIT_DB_DRIVER", "sqlite"),
		DBPath:          getenv("AUDIT_DB_PATH", "/data/audit.db"),
		DBURI:           os.Getenv("AUDIT_DB_URI"),
		Cluster:         getenv("AUDIT_CLUSTER", "main"),
		Namespaces:      os.Getenv("AUDIT_NAMESPACES"),
		Kinds:           os.Getenv("AUDIT_KINDS"),
		Reasons:         os.Getenv("AUDIT_REASONS"),
	}
}

func discoverEverestAPI() string {
	if v := os.Getenv("EVEREST_API_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	host := os.Getenv("EVEREST_SERVICE_HOST")
	port := os.Getenv("EVEREST_SERVICE_PORT")
	if host != "" && port != "" {
		return fmt.Sprintf("http://%s:%s", host, port)
	}
	return "http://everest.everest-system.svc.cluster.local:8080"
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

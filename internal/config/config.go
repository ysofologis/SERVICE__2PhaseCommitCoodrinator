package config

import (
	"os"
	"time"
)

type Config struct {
	Port            string
	TXNDBPath       string
	ShutdownTimeout time.Duration
	ParticipantURLs []string
}

func Load() (*Config, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080"
	}

	txnDB := os.Getenv("TXNDB_PATH")
	if txnDB == "" {
		txnDB = "./txn.db"
	}

	shutdownTimeout := 15 * time.Second
	if st := os.Getenv("SHUTDOWN_TIMEOUT"); st != "" {
		if d, err := time.ParseDuration(st); err == nil {
			shutdownTimeout = d
		}
	}

	// Participant URLs could be comma-separated
	var participantURLs []string
	if urls := os.Getenv("PARTICIPANT_URLS"); urls != "" {
		for _, u := range splitCSV(urls) {
			if u != "" {
				participantURLs = append(participantURLs, u)
			}
		}
	}

	return &Config{
		Port:            port,
		TXNDBPath:       txnDB,
		ShutdownTimeout: shutdownTimeout,
		ParticipantURLs: participantURLs,
	}, nil
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	inQuotes := false
	for _, r := range s {
		switch r {
		case ',':
			if !inQuotes {
				out = append(out, cur)
				cur = ""
			} else {
				cur += string(r)
			}
		case '"':
			inQuotes = !inQuotes
		default:
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

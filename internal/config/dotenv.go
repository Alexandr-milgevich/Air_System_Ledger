package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnvIfPresent loads a small local development .env file, preserving
// existing process environment (which takes precedence in deployment).
// It does not expand variables or execute shell expressions.
func LoadDotEnvIfPresent(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for line := 1; s.Scan(); line++ {
		v := strings.TrimSpace(s.Text())
		if v == "" || strings.HasPrefix(v, "#") {
			continue
		}
		v = strings.TrimPrefix(v, "export ")
		key, val, ok := strings.Cut(v, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("invalid .env line %d", line)
		}
		// Only load variables this application understands. Avoid importing random keys.
		if key != "HTTP_ADDR" && key != "DATABASE_URL" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '\'' && val[len(val)-1] == '\'' || val[0] == '"' && val[len(val)-1] == '"') {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("invalid .env key at line %d: %w", line, err)
			}
		}
	}
	return s.Err()
}

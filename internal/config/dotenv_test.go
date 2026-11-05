package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDotEnvLoadsURLAndPreservesEnvironment(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, ".env")
	if err := os.WriteFile(filename, []byte("# local config\nDATABASE_URL=postgres://app:p@localhost:5454/montage?sslmode=disable\nHTTP_ADDR=:9999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_ADDR", ":7777")
	// Isolated value under test; this process may inherit DATABASE_URL.
	t.Setenv("DATABASE_URL", "")
	if err := os.Unsetenv("DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnvIfPresent(filename); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DATABASE_URL"); got != "postgres://app:p@localhost:5454/montage?sslmode=disable" {
		t.Fatalf("DATABASE_URL=%q", got)
	}
	if got := os.Getenv("HTTP_ADDR"); got != ":7777" {
		t.Fatalf("HTTP_ADDR=%q", got)
	}
}
func TestMissingDotEnvAllowed(t *testing.T) {
	if err := LoadDotEnvIfPresent(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
}

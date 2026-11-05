// Usage: go run ./cmd/admin create-admin <login> <display-name>
// Password is read from a terminal without echo; never pass it as a CLI argument.
package main

import (
	"context"
	"fmt"
	"montage-ledger/internal/auth"
	"montage-ledger/internal/config"
	"montage-ledger/internal/database"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"
)

func main() {
	if len(os.Args) != 4 || os.Args[1] != "create-admin" {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/admin create-admin <login> <display-name>")
		os.Exit(2)
	}
	if e := config.LoadDotEnvIfPresent(".env"); e != nil {
		fmt.Fprintln(os.Stderr, "cannot load .env")
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, "New admin password (at least 12 characters): ")
	raw, e := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if e != nil {
		fmt.Fprintln(os.Stderr, "password must be entered in a terminal")
		os.Exit(1)
	}
	pwd := strings.TrimRight(string(raw), "\r\n")
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	ctx := context.Background()
	db, e := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		fmt.Fprintln(os.Stderr, "database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	if e = (auth.Store{DB: db}).CreateAdmin(ctx, os.Args[2], os.Args[3], pwd); e != nil {
		fmt.Fprintln(os.Stderr, "failed to create admin (check password, unique login, and migration 000002)")
		os.Exit(1)
	}
	fmt.Println("Administrator created")
}

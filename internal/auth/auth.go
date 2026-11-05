package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

const SessionCookie = "montage_session"
const sessionDuration = 12 * time.Hour

type Identity struct {
	ID        string
	Name      string
	Roles     []string
	SessionID string
	CSRFToken string
}
type Store struct{ DB *pgxpool.Pool }

func randomBytes(n int) ([]byte, error) { b := make([]byte, n); _, e := rand.Read(b); return b, e }
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must contain 12-1024 bytes")
	}
	salt, e := randomBytes(16)
	if e != nil {
		return "", e
	}
	h := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}
func VerifyPassword(encoded, password string) bool {
	var m uint32
	var t uint32
	var p uint8
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	if _, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); e != nil || m != 65536 || t != 3 || p != 4 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
func (s Store) CreateAdmin(ctx context.Context, login, name, password string) error {
	login = strings.TrimSpace(login)
	name = strings.TrimSpace(name)
	if login == "" || name == "" {
		return errors.New("login and name required")
	}
	hash, e := HashPassword(password)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO users(login,display_name,password_hash) VALUES($1,$2,$3) RETURNING id`, login, name, hash).Scan(&id)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES($1,'admin')`, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s Store) Login(ctx context.Context, login, password string) (string, string, error) {
	var id, hash string
	var disabled bool
	err := s.DB.QueryRow(ctx, `SELECT id,password_hash,disabled_at IS NOT NULL FROM users WHERE login=$1`, strings.TrimSpace(login)).Scan(&id, &hash, &disabled)
	// Always perform an expensive hash even when the account does not exist.
	if err != nil {
		_, _ = HashPassword("placeholder-password-for-validation")
		return "", "", errors.New("invalid credentials")
	}
	if !VerifyPassword(hash, password) || disabled {
		return "", "", errors.New("invalid credentials")
	}
	token, e := randomBytes(32)
	if e != nil {
		return "", "", e
	}
	csrf, e := randomBytes(32)
	if e != nil {
		return "", "", e
	}
	tokenStr := base64.RawURLEncoding.EncodeToString(token)
	csrfStr := base64.RawURLEncoding.EncodeToString(csrf)
	th := sha256.Sum256([]byte(tokenStr))
	ch := sha256.Sum256([]byte(csrfStr))
	_, e = s.DB.Exec(ctx, `INSERT INTO user_sessions(user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,$4)`, id, th[:], ch[:], time.Now().Add(sessionDuration))
	return tokenStr, csrfStr, e
}
func (s Store) Authenticate(ctx context.Context, token string) (Identity, error) {
	var u Identity
	if len(token) != 43 {
		return u, errors.New("invalid session")
	}
	digest := sha256.Sum256([]byte(token))
	var csrfHash []byte
	e := s.DB.QueryRow(ctx, `SELECT u.id,u.display_name,s.id,s.csrf_hash FROM user_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.disabled_at IS NULL`, digest[:]).Scan(&u.ID, &u.Name, &u.SessionID, &csrfHash)
	if e != nil {
		return Identity{}, errors.New("invalid session")
	}
	rows, e := s.DB.Query(ctx, `SELECT role_code FROM user_roles WHERE user_id=$1`, u.ID)
	if e != nil {
		return Identity{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if e = rows.Scan(&r); e != nil {
			return Identity{}, e
		}
		u.Roles = append(u.Roles, r)
	}
	return u, rows.Err()
}
func (s Store) CheckCSRF(ctx context.Context, sessionID, token string) bool {
	var expected []byte
	err := s.DB.QueryRow(ctx, `SELECT csrf_hash FROM user_sessions WHERE id=$1 AND revoked_at IS NULL AND expires_at>now()`, sessionID).Scan(&expected)
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(expected, sum[:]) == 1
}
func (s Store) Revoke(ctx context.Context, sessionID string) error {
	_, e := s.DB.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, sessionID)
	return e
}
func HasRole(u Identity, roles ...string) bool {
	for _, a := range u.Roles {
		for _, b := range roles {
			if a == b {
				return true
			}
		}
	}
	return false
}

package httpserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"montage-ledger/internal/auth"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// User administration is deliberately restricted to global administrators.
// Project permissions remain distinct from global roles.
type adminUserRow struct {
	ID, Login, Name, Role, Disabled, Projects string
	Self                                      bool
}
type adminUsersPage struct {
	CSRF, Notice string
	Users        []adminUserRow
	Projects     []adminProjectRow
}
type adminProjectRow struct{ ID, Name string }

var adminUsersTemplate = template.Must(template.New("adminUsers").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Пользователи — Монтажные ведомости</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:1200px;margin:2em auto;padding:1em;font:16px system-ui"><p><a href="/app">← Главная</a></p><h1>Пользователи и права доступа</h1><p>Глобальная роль определяет возможности пользователя в системе. Доступ к объектам назначается отдельно.</p>{{if .Notice}}<p role="status">{{.Notice}}</p>{{end}}<section class="panel"><h2>Создать пользователя</h2><form method="post" action="/app/admin/users"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Логин <input name="login" minlength="3" maxlength="120" required pattern="[A-Za-z0-9._-]+"></label><label>Имя <input name="name" maxlength="200" required></label><label>Пароль (не менее 12 символов) <input name="password" type="password" minlength="12" maxlength="1024" autocomplete="new-password" required></label><label>Роль <select name="role"><option value="installer">Монтажник</option><option value="engineer">Инженер</option><option value="manager">Менеджер</option><option value="director">Руководитель</option><option value="admin">Администратор</option></select></label><button type="submit">Создать</button></form></section><section class="panel"><h2>Учётные записи</h2><div style="overflow-x:auto"><table><thead><tr><th>Логин / имя</th><th>Роль</th><th>Статус</th><th>Объекты</th><th>Действия</th></tr></thead><tbody>{{range .Users}}<tr><td><strong>{{.Login}}</strong><p>{{.Name}}</p></td><td>{{.Role}}</td><td>{{.Disabled}}</td><td>{{.Projects}}</td><td><form method="post" action="/app/admin/users/{{.ID}}/update"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>Имя <input name="name" value="{{.Name}}" maxlength="200" required></label><label>Роль <select name="role"><option value="installer" {{if eq .Role "installer"}}selected{{end}}>Монтажник</option><option value="engineer" {{if eq .Role "engineer"}}selected{{end}}>Инженер</option><option value="manager" {{if eq .Role "manager"}}selected{{end}}>Менеджер</option><option value="director" {{if eq .Role "director"}}selected{{end}}>Руководитель</option><option value="admin" {{if eq .Role "admin"}}selected{{end}}>Администратор</option></select></label><button type="submit">Сохранить</button></form><form method="post" action="/app/admin/users/{{.ID}}/password"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>Новый пароль <input name="password" type="password" minlength="12" maxlength="1024" autocomplete="new-password" required></label><button type="submit">Сменить пароль</button></form>{{if not .Self}}<form method="post" action="/app/admin/users/{{.ID}}/status"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit">{{if eq .Disabled "Активен"}}Заблокировать{{else}}Разблокировать{{end}}</button></form>{{end}}</td></tr>{{else}}<tr><td colspan="5">Пользователей пока нет</td></tr>{{end}}</tbody></table></div></section><section class="panel"><h2>Доступ к объектам</h2><p>Назначение ниже заменяет существующее право пользователя на выбранном объекте. Администраторы системы имеют доступ ко всем объектам независимо от записей членства.</p><form method="post" action="/app/admin/access"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Пользователь <select name="user" required>{{range .Users}}<option value="{{.ID}}">{{.Login}} — {{.Name}}</option>{{end}}</select></label><label>Объект <select name="project" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label><label>Право <select name="permission"><option value="read">Чтение</option><option value="write">Редактирование</option><option value="approve">Согласование</option><option value="admin">Администратор объекта</option><option value="remove">Отозвать доступ</option></select></label><button type="submit">Применить</button></form></section><section class="panel"><h2>Журнал администрирования</h2><p>Изменения пользователей и прав фиксируются в PostgreSQL в таблице <code>user_admin_events</code>. События доступны для проверки аудитору через SQL.</p></section></main></body></html>`))

var adminRoles = map[string]bool{"admin": true, "director": true, "engineer": true, "manager": true, "installer": true}

func validLogin(s string) bool {
	if len(s) < 3 || len(s) > 120 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func validName(s string) bool {
	return s == strings.TrimSpace(s) && utf8.ValidString(s) && utf8.RuneCountInString(s) >= 1 && utf8.RuneCountInString(s) <= 200
}
func validAdminForm(w http.ResponseWriter, r *http.Request, u auth.Identity, x web) bool {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", http.StatusForbidden)
		return false
	}
	return x.validPost(w, r, u)
}
func adminWriteError(w http.ResponseWriter, err error) {
	var p *pgconn.PgError
	if errors.As(err, &p) && (p.Code == "23505" || p.Code == "23514" || p.Code == "23503") {
		http.Error(w, "Недопустимые данные или запись уже существует", http.StatusConflict)
		return
	}
	slog.Error("admin user mutation failed", "error", err)
	http.Error(w, "Ошибка изменения учётной записи", http.StatusInternalServerError)
}
func (x web) adminUsersGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := x.db.Query(ctx, `SELECT u.id,u.login,u.display_name,COALESCE((SELECT string_agg(ur.role_code,', ' ORDER BY ur.role_code) FROM user_roles ur WHERE ur.user_id=u.id),''),CASE WHEN u.disabled_at IS NULL THEN 'Активен' ELSE 'Заблокирован' END,COALESCE((SELECT string_agg(p.name||' ('||pm.permission||')',', ' ORDER BY p.name) FROM project_members pm JOIN projects p ON p.id=pm.project_id WHERE pm.user_id=u.id AND p.archived_at IS NULL),'—') FROM users u ORDER BY u.login LIMIT 500`)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	data := adminUsersPage{CSRF: x.csrf(r, u)}
	for rows.Next() {
		var v adminUserRow
		if err = rows.Scan(&v.ID, &v.Login, &v.Name, &v.Role, &v.Disabled, &v.Projects); err != nil {
			break
		}
		v.Self = v.ID == u.ID
		data.Users = append(data.Users, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		adminWriteError(w, err)
		return
	}
	prows, err := x.db.Query(ctx, `SELECT id,name FROM projects WHERE archived_at IS NULL ORDER BY name LIMIT 500`)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	for prows.Next() {
		var p adminProjectRow
		if err = prows.Scan(&p.ID, &p.Name); err != nil {
			break
		}
		data.Projects = append(data.Projects, p)
	}
	if err == nil {
		err = prows.Err()
	}
	prows.Close()
	if err != nil {
		adminWriteError(w, err)
		return
	}
	data.Notice = map[string]string{"created": "Учётная запись создана", "updated": "Данные пользователя сохранены", "password": "Пароль изменён, старые сессии отозваны", "status": "Статус пользователя изменён", "access": "Доступ к объекту обновлён"}[r.URL.Query().Get("ok")]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none';style-src 'unsafe-inline';form-action 'self';base-uri 'none';frame-ancestors 'none'")
	if err = adminUsersTemplate.Execute(w, data); err != nil {
		slog.Error("render user admin", "error", err)
	}
}
func adminRedirect(w http.ResponseWriter, r *http.Request, ok string) {
	http.Redirect(w, r, "/app/admin/users?ok="+ok, http.StatusSeeOther)
}
func (x web) adminUsersCreate(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !validAdminForm(w, r, u, x) {
		return
	}
	login, name, role := strings.TrimSpace(r.PostFormValue("login")), strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("role")
	if !validLogin(login) || !validName(name) || !adminRoles[role] {
		http.Error(w, "Некорректные поля", 400)
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		http.Error(w, "Пароль должен содержать 12–1024 байта", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO users(login,display_name,password_hash) VALUES($1,$2,$3) RETURNING id`, login, name, hash).Scan(&id)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES($1,$2)`, id, role); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = adminAudit(ctx, tx, u.ID, id, "created", fmt.Sprintf("role=%s", role)); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		adminWriteError(w, err)
		return
	}
	adminRedirect(w, r, "created")
}

// The advisory lock serializes all admin role/status changes, preserving the last active administrator.
const adminLockSQL = `SELECT pg_advisory_xact_lock(910129, 1)`

func adminAudit(ctx context.Context, tx pgx.Tx, actor, target, action, detail string) error {
	_, err := tx.Exec(ctx, `INSERT INTO user_admin_events(actor_id,target_user_id,action,details) VALUES($1,$2,$3,$4)`, actor, target, action, detail)
	return err
}
func (x web) adminUsersUpdate(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	if !validAdminForm(w, r, u, x) {
		return
	}
	name, role := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("role")
	if !validName(name) || !adminRoles[role] {
		http.Error(w, "Некорректные поля", 400)
		return
	}
	if id == u.ID && role != "admin" {
		http.Error(w, "Нельзя снять собственные права администратора", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, adminLockSQL); err != nil {
		adminWriteError(w, err)
		return
	}
	var current string
	var disabled bool
	err = tx.QueryRow(ctx, `SELECT display_name,disabled_at IS NOT NULL FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&current, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		adminWriteError(w, err)
		return
	}
	var oldAdmin bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role_code='admin')`, id).Scan(&oldAdmin)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if oldAdmin && role != "admin" && !disabled {
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM users z JOIN user_roles ur ON ur.user_id=z.id AND ur.role_code='admin' WHERE z.disabled_at IS NULL`).Scan(&count)
		if err != nil {
			adminWriteError(w, err)
			return
		}
		if count <= 1 {
			http.Error(w, "Нельзя снять права последнего активного администратора", 409)
			return
		}
	}
	_, err = tx.Exec(ctx, `UPDATE users SET display_name=$2 WHERE id=$1`, id, name)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, id); err != nil {
		adminWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES($1,$2)`, id, role); err != nil {
		adminWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = adminAudit(ctx, tx, u.ID, id, "updated", fmt.Sprintf("role=%s; display_name updated", role)); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		adminWriteError(w, err)
		return
	}
	adminRedirect(w, r, "updated")
}
func (x web) adminUsersPassword(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	if !validAdminForm(w, r, u, x) {
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		http.Error(w, "Пароль должен содержать 12–1024 байта", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, hash)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		http.NotFound(w, r)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = adminAudit(ctx, tx, u.ID, id, "password_changed", "password reset; sessions revoked"); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		adminWriteError(w, err)
		return
	}
	adminRedirect(w, r, "password")
}
func (x web) adminUsersStatus(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	if !validAdminForm(w, r, u, x) {
		return
	}
	if id == u.ID {
		http.Error(w, "Нельзя блокировать или разблокировать собственную учётную запись", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, adminLockSQL); err != nil {
		adminWriteError(w, err)
		return
	}
	var disabled, hasAdmin bool
	err = tx.QueryRow(ctx, `SELECT disabled_at IS NOT NULL FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role_code='admin')`, id).Scan(&hasAdmin); err != nil {
		adminWriteError(w, err)
		return
	}
	if !disabled && hasAdmin {
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM users z JOIN user_roles ur ON ur.user_id=z.id AND ur.role_code='admin' WHERE z.disabled_at IS NULL`).Scan(&count)
		if err != nil {
			adminWriteError(w, err)
			return
		}
		if count <= 1 {
			http.Error(w, "Нельзя блокировать последнего активного администратора", 409)
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET disabled_at=CASE WHEN disabled_at IS NULL THEN now() ELSE NULL END WHERE id=$1`, id); err != nil {
		adminWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		adminWriteError(w, err)
		return
	}
	action := "blocked"
	if disabled {
		action = "unblocked"
	}
	if err = adminAudit(ctx, tx, u.ID, id, action, "sessions revoked"); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		adminWriteError(w, err)
		return
	}
	adminRedirect(w, r, "status")
}
func (x web) adminProjectAccess(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !validAdminForm(w, r, u, x) {
		return
	}
	userID, projectID, permission := r.PostFormValue("user"), r.PostFormValue("project"), r.PostFormValue("permission")
	if !uuidPattern.MatchString(userID) || !uuidPattern.MatchString(projectID) || !(permission == "read" || permission == "write" || permission == "approve" || permission == "admin" || permission == "remove") {
		http.Error(w, "Некорректные поля", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND disabled_at IS NULL) AND EXISTS(SELECT 1 FROM projects WHERE id=$2 AND archived_at IS NULL)`, userID, projectID).Scan(&exists)
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if !exists {
		http.Error(w, "Пользователь заблокирован или объект не найден", 409)
		return
	}
	if permission == "remove" {
		_, err = tx.Exec(ctx, `DELETE FROM project_members WHERE user_id=$1 AND project_id=$2`, userID, projectID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO project_members(user_id,project_id,permission) VALUES($1,$2,$3) ON CONFLICT(project_id,user_id) DO UPDATE SET permission=EXCLUDED.permission`, userID, projectID, permission)
	}
	if err != nil {
		adminWriteError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = adminAudit(ctx, tx, u.ID, userID, "project_access", fmt.Sprintf("project=%s; permission=%s", projectID, permission)); err != nil {
		adminWriteError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		adminWriteError(w, err)
		return
	}
	adminRedirect(w, r, "access")
}

package httpserver

import (
	"context"
	"crypto/subtle"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"montage-ledger/internal/auth"
)

type web struct {
	db   *pgxpool.Pool
	auth auth.Store
}

const page = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} — Монтажные ведомости</title><style>
body{background:#f5f7fb;color:#172033;font:16px/1.5 system-ui,Arial;margin:0}header{background:#17243c;color:white;padding:18px 5%}header strong{font-size:20px}main{max-width:900px;margin:35px auto;padding:0 18px}.panel{background:white;border:1px solid #e2e8f0;border-radius:12px;padding:25px;margin-bottom:22px;box-shadow:0 2px 9px #15253b0a}label{display:block;margin:12px 0 5px;font-weight:600}input{padding:11px;border:1px solid #b8c4d6;border-radius:7px;box-sizing:border-box;width:100%}button{background:#2458b3;color:#fff;border:0;border-radius:7px;padding:10px 16px;cursor:pointer;margin-top:16px}a{color:#2356b1}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #e2e8f0;text-align:left;padding:10px}small{color:#68768d}.error{color:#a32424}nav{display:inline;margin-left:18px}nav a{color:#fff;margin-right:18px}.inline{display:inline}.inline button{background:transparent;border:1px solid #cbd5e1;margin:0;padding:6px 10px}</style><link rel="stylesheet" href="/assets/app.css"></head><body><header><strong>Монтажные ведомости ОВиК</strong>{{if .LoggedIn}}<nav><a href="/app">Главная</a><a href="/app/projects">Объекты</a>{{if .Admin}}<a href="/app/workers">Работники</a>{{end}}{{if .Admin}}<a href="/app/customers">Заказчики</a>{{end}}</nav><form class="inline" action="/logout" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Выход</button></form>{{end}}</header><main><div class="panel"><h1>{{.Title}}</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if eq .View "login"}}<form method="post" action="/login"><label>Логин</label><input name="login" autocomplete="username" required><label>Пароль</label><input name="password" type="password" autocomplete="current-password" required><button>Войти</button></form>{{else if eq .View "home"}}<p>Здравствуйте, {{.Name}}. Данные ниже доступны только по объектам, к которым у вас есть доступ.</p><div class="dashboard-cards"><div><small>Доступные объекты</small><strong>{{.Dashboard.Projects}}</strong></div><div><small>Черновые ревизии</small><strong>{{.Dashboard.Drafts}}</strong></div><div><small>Последние ревизии</small><strong>{{.Dashboard.LatestRevisions}}</strong></div>{{if .Admin}}<div><small>Сумма последних ревизий</small><strong>{{.Dashboard.EstimateTotal}} ₽</strong></div>{{end}}</div><p><a href="/app/projects">Открыть объекты</a>{{if .Admin}} · <a href="/app/customers">Заказчики</a> · <a href="/app/workers">Работники</a>{{end}}</p><p><small>Стоимость суммируется только по последней ревизии каждого объекта, чтобы не учитывать работы повторно. Финансовая сводка доступна администраторам. Начисления и выплаты остаются в отчётах соответствующих ревизий.</small></p>{{else if eq .View "customers"}}<p><small>На этом этапе управление заказчиками доступно только администраторам.</small></p><table><thead><tr><th>Заказчик</th><th>ИНН</th><th>Объектов</th></tr></thead><tbody>{{range .Customers}}<tr><td>{{.Name}}</td><td>{{.INN}}</td><td>{{.Projects}}</td></tr>{{else}}<tr><td colspan="3">Заказчиков пока нет</td></tr>{{end}}</tbody></table></div><div class="panel"><h2>Добавить заказчика</h2><form method="post" action="/app/customers"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Наименование</label><input name="name" maxlength="250" required><label>ИНН (необязательно)</label><input name="inn" maxlength="12"><button>Создать</button></form>{{end}}</div></main></body></html>`

var tmpl = template.Must(template.New("page").Parse(page))

type customer struct {
	Name, INN string
	Projects  int
}
type viewData struct {
	Title, View, Error, Name, CSRF string
	LoggedIn, Admin                bool
	Customers                      []customer
	Dashboard                      dashboardStats
}

func render(w http.ResponseWriter, status int, data viewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}
func secureCookie(r *http.Request) bool { return r.TLS != nil } // Set Secure when HTTPS is active; localhost HTTP is development only.
func cookie(name, value string, secure, httpOnly bool, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}
func (x web) getIdentity(r *http.Request) (auth.Identity, bool) {
	c, e := r.Cookie(auth.SessionCookie)
	if e != nil {
		return auth.Identity{}, false
	}
	u, e := x.auth.Authenticate(r.Context(), c.Value)
	if e != nil {
		return auth.Identity{}, false
	}
	return u, true
}
func (x web) csrf(r *http.Request, u auth.Identity) string {
	c, e := r.Cookie("montage_csrf")
	if e != nil || !x.auth.CheckCSRF(r.Context(), u.SessionID, c.Value) {
		return ""
	}
	return c.Value
}
func (x web) requireUser(next func(http.ResponseWriter, *http.Request, auth.Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := x.getIdentity(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, u)
	}
}
func (x web) loginGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := x.getIdentity(r); ok {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	render(w, 200, viewData{Title: "Вход в систему", View: "login"})
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, r.Host) || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return false
		}
	}
	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func (x web) loginPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Cross-site request denied", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Недопустимый запрос", 400)
		return
	}
	token, csrf, e := x.auth.Login(r.Context(), r.FormValue("login"), r.FormValue("password"))
	if e != nil {
		render(w, 401, viewData{Title: "Вход в систему", View: "login", Error: "Неверный логин или пароль"})
		return
	}
	maxAge := int((12 * time.Hour).Seconds())
	http.SetCookie(w, cookie(auth.SessionCookie, token, secureCookie(r), true, maxAge))
	http.SetCookie(w, cookie("montage_csrf", csrf, secureCookie(r), true, maxAge))
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}
func (x web) logout(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !x.validPost(w, r, u) {
		return
	}
	_ = x.auth.Revoke(r.Context(), u.SessionID)
	http.SetCookie(w, cookie(auth.SessionCookie, "", secureCookie(r), true, -1))
	http.SetCookie(w, cookie("montage_csrf", "", secureCookie(r), true, -1))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (x web) validPost(w http.ResponseWriter, r *http.Request, u auth.Identity) bool {
	if !sameOrigin(r) {
		http.Error(w, "Cross-site request denied", 403)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Недопустимый запрос", 400)
		return false
	}
	token := r.PostFormValue("csrf")
	cookieToken := x.csrf(r, u)
	if token == "" || cookieToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(cookieToken)) != 1 {
		http.Error(w, "CSRF validation failed", 403)
		return false
	}
	return true
}
func (x web) home(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stats, err := x.loadDashboard(ctx, u)
	if err != nil {
		http.Error(w, "Не удалось загрузить главную панель", http.StatusInternalServerError)
		return
	}
	render(w, 200, viewData{Title: "Главная", View: "home", Name: u.Name, LoggedIn: true, Admin: auth.HasRole(u, "admin"), CSRF: x.csrf(r, u), Dashboard: stats})
}
func (x web) customersGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	rows, e := x.db.Query(ctx, `SELECT c.name,coalesce(c.inn,''),count(p.id) FROM customers c LEFT JOIN projects p ON p.customer_id=c.id AND p.archived_at IS NULL WHERE c.archived_at IS NULL GROUP BY c.id ORDER BY c.name LIMIT 200`)
	if e != nil {
		http.Error(w, "Ошибка загрузки", 500)
		return
	}
	defer rows.Close()
	data := viewData{Title: "Заказчики", View: "customers", LoggedIn: true, Admin: true, CSRF: x.csrf(r, u)}
	for rows.Next() {
		var c customer
		if e = rows.Scan(&c.Name, &c.INN, &c.Projects); e != nil {
			http.Error(w, "Ошибка загрузки", 500)
			return
		}
		data.Customers = append(data.Customers, c)
	}
	if rows.Err() != nil {
		http.Error(w, "Ошибка загрузки", 500)
		return
	}
	render(w, 200, data)
}
func (x web) customersPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	inn := strings.TrimSpace(r.PostFormValue("inn"))
	if name == "" || len(name) > 250 || len(inn) > 12 {
		http.Error(w, "Некорректные данные", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_, e := x.db.Exec(ctx, `INSERT INTO customers(name,inn) VALUES($1,NULLIF($2,''))`, name, inn)
	if e != nil {
		http.Error(w, "Не удалось сохранить заказчика", 500)
		return
	}
	http.Redirect(w, r, "/app/customers", http.StatusSeeOther)
}

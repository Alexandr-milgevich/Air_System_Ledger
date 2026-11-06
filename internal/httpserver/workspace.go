package httpserver

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"montage-ledger/internal/auth"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var systemCodes = map[string]string{"supply": "Приточная", "exhaust": "Вытяжная", "supply_exhaust": "Приточно-вытяжная", "conditioning": "Кондиционирование", "cooling": "Холодоснабжение", "drainage": "Дренаж"}

type namedRow struct{ ID, Name, Detail string }
type revisionRow struct {
	ID     string
	Number int
	Status string
}
type workspaceData struct {
	Title, Error, CSRF, View, ProjectID, ProjectName, CustomerName, RevisionID, RevisionStatus, Search string
	Admin, CanWrite                                                                                    bool
	Customers, Projects, Workers, ProjectWorkers, Members, Systems, Locations                          []namedRow
	Revisions                                                                                          []revisionRow
}

const workspaceHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} — Монтажные ведомости</title><style>
body{font:16px/1.5 system-ui,sans-serif;background:#f4f6fa;color:#182236;margin:0}header{background:#182743;color:#fff;padding:18px 5%}header a{color:#fff;margin-right:16px}main{max-width:1080px;margin:28px auto;padding:0 18px}.panel{background:white;padding:24px;border-radius:12px;border:1px solid #dce3eb;margin-bottom:18px}h1{margin:0 0 20px}h2{margin:0 0 12px;font-size:21px}a{color:#245ab0}label{display:block;margin:10px 0 4px;font-weight:600}input,select{box-sizing:border-box;padding:10px;width:100%;max-width:520px;border:1px solid #bcc8d7;border-radius:6px}button{display:block;background:#245ab0;color:white;border:0;padding:11px 19px;margin-top:14px;border-radius:6px;cursor:pointer}table{width:100%;border-collapse:collapse}td,th{padding:10px;border-bottom:1px solid #e3e8f0;text-align:left}.error{color:#a22323}.muted{color:#687488}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:18px}.grid .panel{margin-bottom:0}small{color:#69748a}</style><link rel="stylesheet" href="/assets/app.css"></head><body><header><b>Монтажные ведомости ОВиК</b><nav><a href="/app">Главная</a><a href="/app/customers">Заказчики</a><a href="/app/projects">Объекты</a><a href="/app/workers">Работники</a></nav></header><main><h1>{{.Title}}</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
{{if eq .View "projects"}}
<div class="panel"><h2>Объекты</h2><form method="get" action="/app/projects" class="search-form" role="search"><label for="project-search">Поиск объекта или заказчика</label><div class="search-controls"><input id="project-search" type="search" name="q" value="{{.Search}}" placeholder="Название объекта или заказчика" maxlength="120"><button type="submit">Найти</button><a href="/app/projects">Сбросить</a></div></form><div class="table-scroll"><table><tr><th>Объект</th><th>Заказчик</th></tr>{{range .Projects}}<tr><td><a href="/app/projects/{{.ID}}">{{.Name}}</a></td><td>{{.Detail}}</td></tr>{{else}}<tr><td colspan="2">Объекты не найдены</td></tr>{{end}}</table></div></div>
{{if .Admin}}<div class="panel"><h2>Новый объект</h2><form method="post" action="/app/projects"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Заказчик</label><select name="customer_id" required><option value="">Выберите заказчика</option>{{range .Customers}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select><label>Название объекта</label><input name="name" maxlength="250" required><label>Адрес</label><input name="address" maxlength="500"><button>Создать объект</button></form></div>{{end}}
{{else if eq .View "project"}}
<p class="muted">Заказчик: {{.CustomerName}}</p><div class="panel"><h2>Ревизии монтажных ведомостей</h2><table><tr><th>Номер</th><th>Статус</th></tr>{{range .Revisions}}<tr><td><a href="/app/revisions/{{.ID}}">Ревизия №{{.Number}}</a></td><td>{{.Status}}</td></tr>{{else}}<tr><td colspan="2">Ревизий нет</td></tr>{{end}}</table>{{if .CanWrite}}<form method="post" action="/app/projects/{{.ProjectID}}/revisions"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Создать новую пустую ревизию</button></form>{{end}}</div>
<div class="grid"><div class="panel"><h2>Участники объекта</h2>{{range .Members}}<p>{{.Name}} — {{.Detail}}</p>{{else}}<p>Нет назначений</p>{{end}}{{if .Admin}}<form method="post" action="/app/projects/{{.ProjectID}}/members"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Логин пользователя</label><input name="login" required maxlength="120"><label>Уровень доступа</label><select name="permission"><option value="read">Просмотр</option><option value="write">Редактирование</option><option value="approve">Согласование</option><option value="admin">Администратор объекта</option></select><button>Добавить / изменить доступ</button></form>{{end}}</div>
<div class="panel"><h2>Монтажники объекта</h2>{{range .ProjectWorkers}}<p>{{.Name}}</p>{{else}}<p>Монтажники не закреплены</p>{{end}}{{if .CanWrite}}<form method="post" action="/app/projects/{{.ProjectID}}/workers"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Монтажник</label><select name="worker_id" required><option value="">Выберите работника</option>{{range .Workers}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select><button>Закрепить за объектом</button></form>{{end}}</div></div>
{{else if eq .View "revision"}}
<p><a href="/app/revisions/{{.RevisionID}}/ledger">Открыть редактор монтажной ведомости →</a> · <a href="/app/revisions/{{.RevisionID}}/execution">Факт и начисления</a> · <a href="/app/revisions/{{.RevisionID}}/reports">Сводный отчёт</a></p>
<p class="muted">Объект: <a href="/app/projects/{{.ProjectID}}">{{.ProjectName}}</a> · Статус: {{.RevisionStatus}}</p><div class="grid"><div class="panel"><h2>Инженерные системы</h2><table><tr><th>Код</th><th>Название</th></tr>{{range .Systems}}<tr><td>{{.Name}}</td><td>{{.Detail}}</td></tr>{{else}}<tr><td colspan="2">Систем пока нет</td></tr>{{end}}</table>{{if .CanWrite}}<form action="/app/revisions/{{.RevisionID}}/systems" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Код (например, П-31)</label><input name="code" maxlength="50" required><label>Название</label><input name="name" maxlength="250" required><label>Тип системы</label><select name="type_code"><option value="supply">Приточная</option><option value="exhaust">Вытяжная</option><option value="supply_exhaust">Приточно-вытяжная</option><option value="conditioning">Кондиционирование</option><option value="cooling">Холодоснабжение</option><option value="drainage">Дренаж</option></select><button>Добавить систему</button></form>{{end}}</div><div class="panel"><h2>Места монтажа</h2>{{range .Locations}}<p>{{.Name}}</p>{{else}}<p>Помещения пока не внесены</p>{{end}}{{if .CanWrite}}<form action="/app/revisions/{{.RevisionID}}/locations" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Помещение / зона</label><input name="name" maxlength="250" required><button>Добавить место монтажа</button></form>{{end}}</div></div>
{{else if eq .View "workers"}}
<div class="panel"><h2>Справочник монтажников</h2>{{range .Workers}}<p>{{.Name}}</p>{{else}}<p>Работников пока нет</p>{{end}}</div>{{if .Admin}}<div class="panel"><h2>Новый монтажник</h2><form method="post" action="/app/workers"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>ФИО или отображаемое имя</label><input name="name" maxlength="250" required><button>Добавить работника</button></form></div>{{end}}
{{end}}</main></body></html>`

var workspaceTemplate = template.Must(template.New("workspace").Parse(workspaceHTML))

func workspaceRender(w http.ResponseWriter, status int, data workspaceData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = workspaceTemplate.Execute(w, data)
}
func paramID(w http.ResponseWriter, r *http.Request) string {
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.Error(w, "Некорректный идентификатор", 400)
		return ""
	}
	return id
}
func shortContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 4*time.Second)
}
func (x web) projectPermission(ctx context.Context, id string, u auth.Identity) (string, error) {
	if auth.HasRole(u, "admin") {
		var exists bool
		e := x.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND archived_at IS NULL)`, id).Scan(&exists)
		if e != nil {
			return "", e
		}
		if exists {
			return "admin", nil
		}
		return "", pgx.ErrNoRows
	}
	var permission string
	e := x.db.QueryRow(ctx, `SELECT pm.permission FROM project_members pm JOIN projects p ON p.id=pm.project_id WHERE pm.project_id=$1 AND pm.user_id=$2 AND p.archived_at IS NULL`, id, u.ID).Scan(&permission)
	return permission, e
}
func canEdit(permission string) bool { return permission == "write" || permission == "admin" }
func (x web) projectsGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	ctx, cancel := shortContext(r)
	defer cancel()
	data := workspaceData{Title: "Объекты", View: "projects", Admin: auth.HasRole(u, "admin"), CSRF: x.csrf(r, u)}
	data.Search = strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(data.Search)) > 120 {
		http.Error(w, "Слишком длинный поисковый запрос", http.StatusBadRequest)
		return
	}
	rows, e := x.db.Query(ctx, `SELECT p.id,p.name,c.name FROM projects p JOIN customers c ON c.id=p.customer_id WHERE p.archived_at IS NULL AND ($2::boolean OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.project_id=p.id AND pm.user_id=$1)) AND ($3::text = '' OR p.name ILIKE '%' || $3 || '%' OR c.name ILIKE '%' || $3 || '%') ORDER BY p.name LIMIT 250`, u.ID, data.Admin, data.Search)
	if e != nil {
		http.Error(w, "Не удалось загрузить объекты", 500)
		return
	}
	for rows.Next() {
		var a namedRow
		if e = rows.Scan(&a.ID, &a.Name, &a.Detail); e != nil {
			break
		}
		data.Projects = append(data.Projects, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения", 500)
		return
	}
	if data.Admin {
		rows, e = x.db.Query(ctx, `SELECT id,name FROM customers WHERE archived_at IS NULL ORDER BY name LIMIT 250`)
		if e != nil {
			http.Error(w, "Ошибка загрузки заказчиков", 500)
			return
		}
		for rows.Next() {
			var a namedRow
			if e = rows.Scan(&a.ID, &a.Name); e != nil {
				break
			}
			data.Customers = append(data.Customers, a)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			http.Error(w, "Ошибка чтения", 500)
			return
		}
	}
	workspaceRender(w, 200, data)
}
func (x web) projectsPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	customerID, name, address := r.PostFormValue("customer_id"), strings.TrimSpace(r.PostFormValue("name")), strings.TrimSpace(r.PostFormValue("address"))
	if !uuidPattern.MatchString(customerID) || name == "" || len(name) > 250 || len(address) > 500 {
		http.Error(w, "Некорректные данные", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, e := x.db.Begin(ctx)
	if e != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO projects(customer_id,name,address,created_by) SELECT id,$2,NULLIF($3,''),$4 FROM customers WHERE id=$1 AND archived_at IS NULL RETURNING projects.id`, customerID, name, address, u.ID).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		http.Error(w, "Заказчик не найден", 404)
		return
	}
	if e != nil {
		http.Error(w, "Не удалось создать объект", 500)
		return
	}
	_, e = tx.Exec(ctx, `INSERT INTO project_members(project_id,user_id,permission) VALUES($1,$2,'admin')`, id, u.ID)
	if e != nil {
		http.Error(w, "Ошибка назначения прав", 500)
		return
	}
	if e = tx.Commit(ctx); e != nil {
		http.Error(w, "Ошибка сохранения", 500)
		return
	}
	http.Redirect(w, r, "/app/projects/"+id, http.StatusSeeOther)
}
func (x web) projectGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	permission, e := x.projectPermission(ctx, id, u)
	if e != nil {
		http.Error(w, "Объект не найден или нет доступа", 404)
		return
	}
	data := workspaceData{Title: "Объект", View: "project", ProjectID: id, Admin: permission == "admin", CanWrite: canEdit(permission), CSRF: x.csrf(r, u)}
	e = x.db.QueryRow(ctx, `SELECT p.name,c.name FROM projects p JOIN customers c ON c.id=p.customer_id WHERE p.id=$1`, id).Scan(&data.ProjectName, &data.CustomerName)
	if e != nil {
		http.Error(w, "Не найден", 404)
		return
	}
	data.Title = data.ProjectName
	rows, e := x.db.Query(ctx, `SELECT id,number,status FROM project_revisions WHERE project_id=$1 ORDER BY number DESC`, id)
	if e != nil {
		http.Error(w, "Ошибка загрузки ревизий", 500)
		return
	}
	for rows.Next() {
		var a revisionRow
		if e = rows.Scan(&a.ID, &a.Number, &a.Status); e != nil {
			break
		}
		data.Revisions = append(data.Revisions, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения ревизий", 500)
		return
	}
	rows, e = x.db.Query(ctx, `SELECT us.display_name,pm.permission FROM project_members pm JOIN users us ON us.id=pm.user_id WHERE pm.project_id=$1 ORDER BY us.display_name`, id)
	if e != nil {
		http.Error(w, "Ошибка участников", 500)
		return
	}
	for rows.Next() {
		var a namedRow
		if e = rows.Scan(&a.Name, &a.Detail); e != nil {
			break
		}
		data.Members = append(data.Members, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения участников", 500)
		return
	}
	rows, e = x.db.Query(ctx, `SELECT w.id,w.display_name FROM project_workers pw JOIN workers w ON w.id=pw.worker_id WHERE pw.project_id=$1 ORDER BY w.display_name`, id)
	if e != nil {
		http.Error(w, "Ошибка монтажников", 500)
		return
	}
	for rows.Next() {
		var a namedRow
		if e = rows.Scan(&a.ID, &a.Name); e != nil {
			break
		}
		data.ProjectWorkers = append(data.ProjectWorkers, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения монтажников", 500)
		return
	}
	if data.CanWrite {
		rows, e = x.db.Query(ctx, `SELECT id,display_name FROM workers WHERE active=true ORDER BY display_name LIMIT 300`)
		if e != nil {
			http.Error(w, "Ошибка справочника", 500)
			return
		}
		for rows.Next() {
			var a namedRow
			if e = rows.Scan(&a.ID, &a.Name); e != nil {
				break
			}
			data.Workers = append(data.Workers, a)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			http.Error(w, "Ошибка чтения работников", 500)
			return
		}
	}
	workspaceRender(w, 200, data)
}
func (x web) revisionsPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	permission, e := x.projectPermission(ctx, id, u)
	if e != nil || !canEdit(permission) {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	tx, e := x.db.Begin(ctx)
	if e != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	// Serialize revision number allocation per project.
	var locked string
	e = tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, id).Scan(&locked)
	if e != nil {
		http.Error(w, "Объект не найден", 404)
		return
	}
	var revisionID string
	e = tx.QueryRow(ctx, `INSERT INTO project_revisions(project_id,number,created_by) SELECT $1,COALESCE(MAX(number),0)+1,$2 FROM project_revisions WHERE project_id=$1 RETURNING id`, id, u.ID).Scan(&revisionID)
	if e != nil {
		http.Error(w, "Ошибка создания ревизии", 500)
		return
	}
	if e = tx.Commit(ctx); e != nil {
		http.Error(w, "Ошибка сохранения", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+revisionID, http.StatusSeeOther)
}
func (x web) revisionAccess(ctx context.Context, id string, u auth.Identity) (string, string, string, error) {
	var projectID, status, name string
	e := x.db.QueryRow(ctx, `SELECT r.project_id,r.status,p.name FROM project_revisions r JOIN projects p ON p.id=r.project_id WHERE r.id=$1`, id).Scan(&projectID, &status, &name)
	if e != nil {
		return "", "", "", e
	}
	permission, e := x.projectPermission(ctx, projectID, u)
	return projectID, status, name, func() error {
		if e != nil {
			return e
		}
		if permission == "" {
			return pgx.ErrNoRows
		}
		return nil
	}()
}
func (x web) revisionGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	projectID, status, projectName, e := x.revisionAccess(ctx, id, u)
	if e != nil {
		http.Error(w, "Ревизия не найдена или нет доступа", 404)
		return
	}
	permission, e := x.projectPermission(ctx, projectID, u)
	if e != nil {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	data := workspaceData{Title: "Ревизия монтажной ведомости", View: "revision", ProjectID: projectID, ProjectName: projectName, RevisionID: id, RevisionStatus: status, CanWrite: canEdit(permission) && status == "draft", CSRF: x.csrf(r, u)}
	rows, e := x.db.Query(ctx, `SELECT id,code,name FROM project_systems WHERE revision_id=$1 ORDER BY sort_order,code`, id)
	if e != nil {
		http.Error(w, "Ошибка систем", 500)
		return
	}
	for rows.Next() {
		var a namedRow
		if e = rows.Scan(&a.ID, &a.Name, &a.Detail); e != nil {
			break
		}
		data.Systems = append(data.Systems, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения систем", 500)
		return
	}
	rows, e = x.db.Query(ctx, `SELECT id,name FROM project_locations WHERE revision_id=$1 ORDER BY name`, id)
	if e != nil {
		http.Error(w, "Ошибка мест монтажа", 500)
		return
	}
	for rows.Next() {
		var a namedRow
		if e = rows.Scan(&a.ID, &a.Name); e != nil {
			break
		}
		data.Locations = append(data.Locations, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения мест", 500)
		return
	}
	workspaceRender(w, 200, data)
}
func (x web) revisionWrite(w http.ResponseWriter, r *http.Request, u auth.Identity) (string, bool) {
	id := paramID(w, r)
	if id == "" {
		return "", false
	}
	if !x.validPost(w, r, u) {
		return "", false
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	projectID, status, _, e := x.revisionAccess(ctx, id, u)
	if e != nil {
		http.Error(w, "Нет доступа", 404)
		return "", false
	}
	permission, e := x.projectPermission(ctx, projectID, u)
	if e != nil || !canEdit(permission) {
		http.Error(w, "Доступ запрещён", 403)
		return "", false
	}
	if status != "draft" {
		http.Error(w, "Редактировать можно только черновик", 409)
		return "", false
	}
	return id, true
}
func (x web) systemsPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	code, name, typ := strings.TrimSpace(r.PostFormValue("code")), strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("type_code")
	if code == "" || name == "" || len(code) > 50 || len(name) > 250 || systemCodes[typ] == "" {
		http.Error(w, "Некорректная система", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	// Guard_revision_content locks revision and rejects final statuses in DB.
	_, e := x.db.Exec(ctx, `INSERT INTO project_systems(revision_id,code,name,type_code,sort_order) VALUES($1,$2,$3,$4,(SELECT COUNT(*) FROM project_systems WHERE revision_id=$1))`, id, code, name, typ)
	if e != nil {
		http.Error(w, "Система уже существует или ревизия закрыта", 409)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+id, http.StatusSeeOther)
}
func (x web) locationsPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > 250 {
		http.Error(w, "Некорректное название", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	_, e := x.db.Exec(ctx, `INSERT INTO project_locations(revision_id,name) VALUES($1,$2)`, id, name)
	if e != nil {
		http.Error(w, "Место уже существует или ревизия закрыта", 409)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+id, http.StatusSeeOther)
}
func (x web) membersPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	permission, e := x.projectPermission(ctx, id, u)
	if e != nil || permission != "admin" {
		http.Error(w, "Только администратор объекта может назначать доступ", 403)
		return
	}
	login, access := strings.TrimSpace(r.PostFormValue("login")), r.PostFormValue("permission")
	if login == "" || len(login) > 120 || (access != "read" && access != "write" && access != "approve" && access != "admin") {
		http.Error(w, "Некорректные данные", 400)
		return
	}
	// Only an application administrator can assign administrative project privileges.
	if access == "admin" && !auth.HasRole(u, "admin") {
		http.Error(w, "Недостаточно прав для назначения администратора", 403)
		return
	}
	tag, e := x.db.Exec(ctx, `INSERT INTO project_members(project_id,user_id,permission) SELECT $1,id,$3 FROM users WHERE login=$2 AND disabled_at IS NULL ON CONFLICT(project_id,user_id) DO UPDATE SET permission=EXCLUDED.permission`, id, login, access)
	if e != nil {
		http.Error(w, "Ошибка назначения", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "Пользователь не найден", 404)
		return
	}
	http.Redirect(w, r, "/app/projects/"+id, http.StatusSeeOther)
}
func (x web) workersGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin", "manager", "engineer") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	rows, e := x.db.Query(ctx, `SELECT id,display_name FROM workers WHERE active=true ORDER BY display_name LIMIT 300`)
	if e != nil {
		http.Error(w, "Ошибка справочника", 500)
		return
	}
	data := workspaceData{Title: "Работники", View: "workers", Admin: auth.HasRole(u, "admin"), CSRF: x.csrf(r, u)}
	for rows.Next() {
		var a namedRow
		if e = rows.Scan(&a.ID, &a.Name); e != nil {
			break
		}
		data.Workers = append(data.Workers, a)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения", 500)
		return
	}
	workspaceRender(w, 200, data)
}
func (x web) workersPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > 250 {
		http.Error(w, "Некорректное имя", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	_, e := x.db.Exec(ctx, `INSERT INTO workers(display_name) VALUES($1)`, name)
	if e != nil {
		http.Error(w, "Не удалось добавить работника", 500)
		return
	}
	http.Redirect(w, r, "/app/workers", http.StatusSeeOther)
}
func (x web) projectWorkersPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	workerID := r.PostFormValue("worker_id")
	if !uuidPattern.MatchString(workerID) {
		http.Error(w, "Некорректный монтажник", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	permission, e := x.projectPermission(ctx, id, u)
	if e != nil || !canEdit(permission) {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	tag, e := x.db.Exec(ctx, `INSERT INTO project_workers(project_id,worker_id,added_by) SELECT $1,id,$3 FROM workers WHERE id=$2 AND active=true ON CONFLICT(project_id,worker_id) DO NOTHING`, id, workerID, u.ID)
	if e != nil {
		http.Error(w, "Не удалось назначить работника", 500)
		return
	}
	if tag.RowsAffected() == 0 { /* existing assignments are idempotent */
	}
	http.Redirect(w, r, "/app/projects/"+id, http.StatusSeeOther)
}

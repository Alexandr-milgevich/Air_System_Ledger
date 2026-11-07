package httpserver

import (
	"context"
	"html/template"
	"montage-ledger/internal/auth"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Catalog values are templates only: a revision line snapshots name/rule/price at creation.
type catalogRow struct {
	ID, Name, Category, Unit, Mode, Coefficient, Price, Width, Height, Diameter string
	Active                                                                      bool
}
type catalogPage struct {
	CSRF, Notice string
	Rows         []catalogRow
	Admin        bool
}

var catalogTemplate = template.Must(template.New("catalog").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Справочник монтажных работ</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:1180px;margin:2em auto;padding:1em;font:15px system-ui"><p><a href="/app">← Главная</a></p><h1>Справочник монтажных работ и расценок</h1><p>Цены и коэффициенты применяются только к новым строкам. Уже созданные ведомости не пересчитываются автоматически.</p>{{if .Admin}}<section class="panel"><h2>Добавить позицию</h2><form action="/app/catalog" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><p><label>Название <input name="name" maxlength="250" required></label></p><p><label>Категория <input name="category" maxlength="80" required placeholder="Воздуховоды"></label></p><p><label>Исходная единица <select name="unit"><option value="pcs">шт.</option><option value="m">пог. м</option><option value="m2">м²</option></select></label></p><p><label>Способ расчёта <select name="mode"><option value="direct">Прямой</option><option value="coefficient">Количество × коэффициент</option><option value="reference_coefficient">Справочный коэффициент</option><option value="geometry_rectangular">Прямоугольный воздуховод</option><option value="geometry_round">Круглый воздуховод</option></select></label></p><p><label>Коэффициент <input name="coefficient" value="1" inputmode="decimal"></label></p><p><label>Цена за расчётную единицу, ₽ <input name="price" value="0" required inputmode="decimal"></label></p><p><label>Ширина, мм <input name="width_mm" inputmode="decimal"></label></p><p><label>Высота, мм <input name="height_mm" inputmode="decimal"></label></p><p><label>Диаметр, мм <input name="diameter_mm" inputmode="decimal"></label></p><button>Добавить в каталог</button></form></section>{{end}}<section class="panel"><h2>Позиции каталога</h2><table><thead><tr><th>Категория / название</th><th>Ед.</th><th>Расчёт</th><th>Коэф.</th><th>Цена</th><th>Габариты (мм)</th><th>Состояние</th>{{if .Admin}}<th>Действие</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr><td><b>{{.Category}}</b> · {{.Name}}</td><td>{{.Unit}}</td><td>{{.Mode}}</td><td>{{.Coefficient}}</td><td>{{.Price}}</td><td>{{.Width}} × {{.Height}} / Ø{{.Diameter}}</td><td>{{if .Active}}Активна{{else}}Скрыта{{end}}</td>{{if $.Admin}}<td><form method="post" action="/app/catalog/{{.ID}}/price"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>Цена ₽ <input name="price" value="{{.Price}}" required inputmode="decimal" style="width:90px"></label><label>Коэффициент <input name="coefficient" value="{{.Coefficient}}" inputmode="decimal" style="width:90px"></label><button>Обновить</button></form><form method="post" action="/app/catalog/{{.ID}}/status"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>{{if .Active}}Скрыть{{else}}Активировать{{end}}</button></form></td>{{end}}</tr>{{else}}<tr><td colspan="8">Пока пусто</td></tr>{{end}}</tbody></table></section></main></body></html>`))

// Only rules supported by the existing ledger editor are catalogable.
func catalogInput(name, category, unit, mode, qty, coeff, price, width, height, diameter string) (itemInput, string, error) {
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	if name == "" || utf8.RuneCountInString(name) > 250 || category == "" || utf8.RuneCountInString(category) > 80 {
		return itemInput{}, "", errCatalogInput
	}
	if mode != "direct" && mode != "coefficient" && mode != "reference_coefficient" && mode != "geometry_rectangular" && mode != "geometry_round" {
		return itemInput{}, "", errCatalogInput
	}
	i := itemInput{Name: name, Unit: unit, Mode: mode, Quantity: qty, Coefficient: coeff, Price: price, WidthMM: width, HeightMM: height, DiameterMM: diameter}
	if _, _, err := validateItem(i); err != nil {
		return itemInput{}, "", err
	}
	return i, category, nil
}

var errCatalogInput = &catalogInputError{}

type catalogInputError struct{}

func (*catalogInputError) Error() string {
	return "Недопустимая позиция каталога"
}

func (x web) catalogGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := x.db.Query(ctx, `SELECT id::text,name,category_code,input_unit_code,rule_type,COALESCE(coefficient::text,''),unit_price::text,COALESCE(width_mm::text,''),COALESCE(height_mm::text,''),COALESCE(diameter_mm::text,''),active FROM work_catalog ORDER BY active DESC,category_code,name LIMIT 1000`)
	if err != nil {
		http.Error(w, "Не удалось загрузить каталог", 500)
		return
	}
	data := catalogPage{CSRF: x.csrf(r, u), Admin: auth.HasRole(u, "admin")}
	for rows.Next() {
		var v catalogRow
		if err = rows.Scan(&v.ID, &v.Name, &v.Category, &v.Unit, &v.Mode, &v.Coefficient, &v.Price, &v.Width, &v.Height, &v.Diameter, &v.Active); err != nil {
			break
		}
		data.Rows = append(data.Rows, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка чтения каталога", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_ = catalogTemplate.Execute(w, data)
}
func (x web) catalogCreate(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	i, category, err := catalogInput(r.PostFormValue("name"), r.PostFormValue("category"), r.PostFormValue("unit"), r.PostFormValue("mode"), "1", resultInput(r.PostFormValue("coefficient")), resultInput(r.PostFormValue("price")), resultInput(r.PostFormValue("width_mm")), resultInput(r.PostFormValue("height_mm")), resultInput(r.PostFormValue("diameter_mm")))
	if err != nil {
		http.Error(w, "Некорректная позиция: "+err.Error(), 400)
		return
	}
	// Derive billable unit from actual calculation mode, not client data.
	_, billable, _ := validateItem(i)
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO work_catalog(name,category_code,input_unit_code,result_unit_code,billable_unit_code,rule_type,coefficient,unit_price,width_mm,height_mm,diameter_mm,created_by)
 VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id::text`, i.Name, category, i.Unit, billable, i.Mode, nullableCoefficient(i.Coefficient), i.Price, nullableCatalogNum(i.WidthMM), nullableCatalogNum(i.HeightMM), nullableCatalogNum(i.DiameterMM), u.ID).Scan(&id)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO work_catalog_events(catalog_id,actor_id,action,after_state) SELECT id,$2,'create',to_jsonb(c) FROM work_catalog c WHERE c.id=$1`, id, u.ID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		http.Error(w, "Не удалось сохранить позицию", 500)
		return
	}
	http.Redirect(w, r, "/app/catalog", http.StatusSeeOther)
}
func nullableCatalogNum(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (x web) catalogStatus(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.Error(w, "Некорректная позиция", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	var old bool
	err = tx.QueryRow(ctx, `SELECT active FROM work_catalog WHERE id=$1 FOR UPDATE`, id).Scan(&old)
	if err == pgx.ErrNoRows {
		http.Error(w, "Позиция не найдена", 404)
		return
	}
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE work_catalog SET active=$2,updated_at=now() WHERE id=$1`, id, !old)
	if err == nil {
		action := "activate"
		if old {
			action = "deactivate"
		}
		_, err = tx.Exec(ctx, `INSERT INTO work_catalog_events(catalog_id,actor_id,action,before_state,after_state) SELECT c.id,$2,$3,jsonb_build_object('active',$4),to_jsonb(c) FROM work_catalog c WHERE c.id=$1`, id, u.ID, action, old)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		http.Error(w, "Не удалось изменить позицию", 500)
		return
	}
	http.Redirect(w, r, "/app/catalog", http.StatusSeeOther)
}

// Price/coefficient updates apply to FUTURE items only. Existing item snapshots are not touched.
func (x web) catalogPriceUpdate(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !auth.HasRole(u, "admin") {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	id := r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		http.Error(w, "Некорректная позиция", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	var c catalogRow
	err = tx.QueryRow(ctx, `SELECT name,category_code,input_unit_code,rule_type,COALESCE(coefficient::text,''),unit_price::text,COALESCE(width_mm::text,''),COALESCE(height_mm::text,''),COALESCE(diameter_mm::text,''),active FROM work_catalog WHERE id=$1 FOR UPDATE`, id).Scan(&c.Name, &c.Category, &c.Unit, &c.Mode, &c.Coefficient, &c.Price, &c.Width, &c.Height, &c.Diameter, &c.Active)
	if err == pgx.ErrNoRows {
		http.Error(w, "Позиция не найдена", 404)
		return
	}
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	proposedPrice := resultInput(r.PostFormValue("price"))
	proposedCoeff := resultInput(r.PostFormValue("coefficient"))
	if proposedCoeff == "" {
		proposedCoeff = c.Coefficient
	}
	_, _, err = catalogInput(c.Name, c.Category, c.Unit, c.Mode, "1", proposedCoeff, proposedPrice, c.Width, c.Height, c.Diameter)
	if err != nil {
		http.Error(w, "Некорректная цена или коэффициент", 400)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE work_catalog SET unit_price=$2,coefficient=$3,updated_at=now() WHERE id=$1`, id, proposedPrice, nullableCatalogNum(proposedCoeff))
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO work_catalog_events(catalog_id,actor_id,action,before_state,after_state) SELECT c.id,$2,'update',jsonb_build_object('unit_price',$3::numeric,'coefficient',$4::numeric),to_jsonb(c) FROM work_catalog c WHERE c.id=$1`, id, u.ID, c.Price, nullableCatalogNum(c.Coefficient))
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		http.Error(w, "Ошибка изменения цены", 500)
		return
	}
	http.Redirect(w, r, "/app/catalog", http.StatusSeeOther)
}

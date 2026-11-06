package httpserver

import (
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"montage-ledger/internal/auth"
	"montage-ledger/internal/calculation"
)

type ledgerSystem struct {
	ID, Code, Name, Total string
	Items                 []ledgerItem
	Visible               bool
}
type ledgerItem struct{ ID, Name, Quantity, InputUnit, Mode, Coefficient, Calculated, ResultUnit, UnitPrice, Amount, Location, LocationID, WidthMM, HeightMM, DiameterMM, SourceID, IncludeCSV, ExcludeCSV string }
type ledgerView struct {
	RevisionID, ProjectID, ProjectName, Status, CSRF, Error, Total string
	CanWrite                                                       bool
	FilterSystem, FilterQuery                                      string
	VisibleCount, TotalCount                                       int
	Systems                                                        []ledgerSystem
	Locations                                                      []namedRow
	AllItems                                                       []ledgerItem
}

const ledgerHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Редактор ведомости</title><style>
body{margin:0;background:#f4f6fa;color:#19263a;font:15px/1.5 system-ui,sans-serif}header{background:#182743;color:white;padding:17px 4%}header a{color:white}main{max-width:1420px;margin:25px auto;padding:0 18px}.panel{background:white;border:1px solid #dce3eb;border-radius:11px;padding:20px;margin-bottom:18px}h1{font-size:26px}h2{font-size:21px}h3{font-size:18px}a{color:#245ab0}label{display:block;font-weight:600;margin:7px 0 3px}input,select{box-sizing:border-box;padding:8px;border:1px solid #bcc8d7;border-radius:5px;max-width:100%;width:100%}button{background:#245ab0;color:white;border:0;border-radius:5px;padding:9px 13px;cursor:pointer}.danger{background:#a23a3a}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px}.row{display:flex;align-items:center;gap:12px;flex-wrap:wrap}table{width:100%;border-collapse:collapse}th,td{border-bottom:1px solid #e1e6ee;text-align:left;padding:9px}th{background:#f1f4f9}td.num{text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap}.scroll{overflow-x:auto}.muted{color:#66758a}.error{color:#9c1e1e}.total{font-size:21px;font-weight:700}.small{font-size:12px}.inline{display:inline}.inline button{padding:5px 9px}.editform{margin-top:7px;border-top:1px dashed #dde4ec;padding-top:10px}details{margin-top:8px}summary{cursor:pointer;color:#245ab0}.nowrap{white-space:nowrap}</style><link rel="stylesheet" href="/assets/app.css"></head><body><header><b>Монтажные ведомости ОВиК</b> · <a href="/app/revisions/{{.RevisionID}}">К ревизии</a> · <a href="/app/revisions/{{.RevisionID}}/execution">Факт и начисления</a> · <a href="/app/revisions/{{.RevisionID}}/reports">Сводный отчёт</a> · <a href="/app/revisions/{{.RevisionID}}/xlsx">Импорт XLSX</a> · <a href="/app/revisions/{{.RevisionID}}/xlsx/export">Экспорт XLSX</a> · <a href="/app/projects/{{.ProjectID}}">К объекту</a></header><main><h1>Редактор монтажной ведомости</h1><p class="muted">{{.ProjectName}} · {{.Status}} · {{if .CanWrite}}Редактирование доступно{{else}}Только просмотр{{end}}</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
{{if .CanWrite}}<section class="panel"><h2>Добавить монтажную работу</h2><form action="/app/revisions/{{.RevisionID}}/items" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><div class="grid"><div><label>Система</label><select name="system_id" required>{{range .Systems}}<option value="{{.ID}}">{{.Code}} · {{.Name}}</option>{{end}}</select></div><div><label>Название работы</label><input name="name" required maxlength="250" placeholder="Воздуховод 400×200"></div><div><label>Исходное количество</label><input name="quantity" required value="1" inputmode="decimal"></div><div><label>Единица</label><select name="unit"><option value="m">пог. м</option><option value="pcs">шт.</option><option value="m2">м²</option></select></div><div><label>Расчёт</label><select name="mode"><option value="coefficient">Количество × коэффициент (м²)</option><option value="direct">Напрямую (шт., м, м²)</option><option value="geometry_rectangular">Прямоугольный воздуховод (м²)</option><option value="geometry_round">Круглый воздуховод (м²)</option><option value="reference_coefficient">Справочный коэффициент (м²/шт.)</option><option value="sum_items">Изоляция: одна строка × коэффициент</option><option value="sum_multi">Изоляция: сумма площадей минус исключения × коэффициент</option></select></div><div><label>Ширина A (мм)</label><input name="width_mm" placeholder="400"></div><div><label>Высота B (мм)</label><input name="height_mm" placeholder="200"></div><div><label>Диаметр D (мм)</label><input name="diameter_mm" placeholder="200"></div><div><label>Источник для изоляции</label><select name="source_item_id"><option value="">Не выбран</option>{{range .AllItems}}<option value="{{.ID}}">{{.Name}} ({{.Calculated}} {{.ResultUnit}})</option>{{end}}</select></div><div><label>Сложить площади (Ctrl/Shift для выбора нескольких)</label><select name="include_ids" multiple size="5">{{range .AllItems}}<option value="{{.ID}}">{{.Name}} ({{.Calculated}} {{.ResultUnit}})</option>{{end}}</select></div><div><label>Вычесть площади</label><select name="exclude_ids" multiple size="5">{{range .AllItems}}<option value="{{.ID}}">{{.Name}} ({{.Calculated}} {{.ResultUnit}})</option>{{end}}</select></div><div><label>Коэффициент (м²/ед.)</label><input name="coefficient" value="1" inputmode="decimal"></div><div><label>Цена за расчётную единицу, ₽</label><input name="price" value="0" required inputmode="decimal"></div><div><label>Место монтажа</label><select name="location_id"><option value="">Не указано</option>{{range .Locations}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div></div><button>Добавить позицию</button></form><p class="muted small">Для геометрии выберите единицу «пог. м» и заполните размеры в мм. Для изоляции выберите строку-источник (м²), укажите коэффициент и цену. В режиме «Напрямую» цена относится к исходной единице. В режиме «Количество × коэффициент» результат тарифицируется в м². Поля коэффициента и цены допускают десятичную запятую.</p></section>{{end}}
<section class="panel"><h2>Фильтрация работ</h2><form class="ledger-filters" action="/app/revisions/{{.RevisionID}}/ledger" method="get"><label>Система<select name="system"><option value="">Все системы</option>{{range .Systems}}<option value="{{.ID}}" {{if eq $.FilterSystem .ID}}selected{{end}}>{{.Code}} · {{.Name}}</option>{{end}}</select></label><label>Поиск по работе или месту монтажа<input type="search" name="q" maxlength="120" value="{{.FilterQuery}}" placeholder="Например, воздуховод"></label><button type="submit">Применить</button><a href="/app/revisions/{{.RevisionID}}/ledger">Сбросить</a></form><p class="muted">Показано {{.VisibleCount}} из {{.TotalCount}} строк. Итоги систем и ведомости не зависят от фильтров.</p></section>
{{if .CanWrite}}<section class="panel"><h2>Массовое редактирование</h2><p><a href="/app/revisions/{{.RevisionID}}/bulk/history">Журнал массовых изменений и откат</a></p><p class="muted">Выберите строки в таблицах ниже, задайте операцию и проверьте изменения до сохранения (не более 100 строк).</p><form id="bulk-preview" method="post" action="/app/revisions/{{.RevisionID}}/bulk/preview"><input type="hidden" name="csrf" value="{{.CSRF}}"><div class="grid"><div><label>Операция</label><select name="action"><option value="price_percent">Изменить цену на % (например, 10 или -5)</option><option value="price_fixed">Установить цену, ₽</option><option value="location">Назначить место монтажа</option></select></div><div><label>Значение цены или процента</label><input name="value" value="10" inputmode="decimal"></div><div><label>Место монтажа</label><select name="location_id"><option value="">Не указано</option>{{range .Locations}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div></div><button type="submit">Предпросмотр выбранных строк</button></form></section>{{end}}
{{range .Systems}}{{if .Visible}}<section class="panel"><h2>{{.Code}} — {{.Name}}</h2><div class="scroll"><table><thead><tr>{{if $.CanWrite}}<th>Выбор</th>{{end}}<th>Работа</th><th>Количество</th><th>Ед.</th><th>Коэф.</th><th>Расчётное количество</th><th>Ед. тарифа</th><th>Цена, ₽</th><th>Сумма, ₽</th><th>Место</th>{{if $.CanWrite}}<th>Действия</th>{{end}}</tr></thead><tbody>{{range .Items}}<tr>{{if $.CanWrite}}<td><input type="checkbox" name="item_ids" value="{{.ID}}" form="bulk-preview" aria-label="Выбрать {{.Name}}"></td>{{end}}<td>{{.Name}}</td><td class="num">{{.Quantity}}</td><td>{{.InputUnit}}</td><td class="num">{{.Coefficient}}</td><td class="num">{{.Calculated}}</td><td>{{.ResultUnit}}</td><td class="num">{{.UnitPrice}}</td><td class="num">{{.Amount}}</td><td>{{.Location}}</td>{{if $.CanWrite}}<td><details><summary>Изменить</summary><div class="editform"><form method="post" action="/app/revisions/{{$.RevisionID}}/items/{{.ID}}"><input type="hidden" name="csrf" value="{{$.CSRF}}"><div><label>Название</label><input name="name" value="{{.Name}}" maxlength="250" required><label>Количество</label><input name="quantity" value="{{.Quantity}}" required><label>Единица</label><select name="unit"><option value="{{.InputUnit}}">{{.InputUnit}}</option><option value="m">m</option><option value="pcs">pcs</option><option value="m2">m2</option></select><label>Расчёт</label><select name="mode"><option value="{{.Mode}}">{{.Mode}}</option><option value="direct">direct</option><option value="coefficient">coefficient</option><option value="geometry_rectangular">geometry_rectangular</option><option value="geometry_round">geometry_round</option><option value="reference_coefficient">reference_coefficient</option><option value="sum_items">sum_items</option><option value="sum_multi">sum_multi</option></select><label>Ширина A (мм)</label><input name="width_mm" value="{{.WidthMM}}"><label>Высота B (мм)</label><input name="height_mm" value="{{.HeightMM}}"><label>Диаметр D (мм)</label><input name="diameter_mm" value="{{.DiameterMM}}"><label>Источник для изоляции</label><select name="source_item_id"><option value="{{.SourceID}}">Оставить текущий источник</option><option value="">Не выбран</option>{{range $.AllItems}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select>{{ $current := . }}<label>Сложить площади (Ctrl/Shift)</label><select name="include_ids" multiple size="5">{{range $.AllItems}}<option value="{{.ID}}" {{if $current.Included .ID}}selected{{end}}>{{.Name}} ({{.Calculated}} {{.ResultUnit}})</option>{{end}}</select><label>Вычесть площади</label><select name="exclude_ids" multiple size="5">{{range $.AllItems}}<option value="{{.ID}}" {{if $current.Excluded .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select><label>Коэффициент</label><input name="coefficient" value="{{.Coefficient}}"><label>Цена</label><input name="price" value="{{.UnitPrice}}" required><label>Место</label><select name="location_id"><option value="{{.LocationID}}">Оставить текущее место</option><option value="">Не указано</option>{{range $.Locations}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select><button>Сохранить</button></div></form><form method="post" action="/app/revisions/{{$.RevisionID}}/items/{{.ID}}/delete" ><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="danger">Удалить</button></form></details></td>{{end}}</tr>{{else}}<tr><td colspan="11">Работ пока нет</td></tr>{{end}}</tbody><tfoot><tr><th colspan="{{if $.CanWrite}}8{{else}}7{{end}}">Итого по системе</th><th class="num">{{.Total}} ₽</th><th colspan="2"></th></tr></tfoot></table></div></section>{{end}}{{else}}<div class="panel">Сначала добавьте хотя бы одну систему в ревизию.</div>{{end}}<section class="panel row"><strong>Общий итог по ведомости</strong><span class="total">{{.Total}} ₽</span></section></main></body></html>`

var ledgerTemplate = template.Must(template.New("ledger").Parse(ledgerHTML))

func (it ledgerItem) Included(id string) bool {
	for _, x := range strings.Split(it.IncludeCSV, ",") {
		if x == id {
			return true
		}
	}
	return false
}
func (it ledgerItem) Excluded(id string) bool {
	for _, x := range strings.Split(it.ExcludeCSV, ",") {
		if x == id {
			return true
		}
	}
	return false
}

func ledgerRender(w http.ResponseWriter, code int, data ledgerView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(code)
	_ = ledgerTemplate.Execute(w, data)
}
func (x web) ledgerGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	projectID, status, name, e := x.revisionAccess(ctx, id, u)
	if e != nil {
		http.Error(w, "Ведомость не найдена", 404)
		return
	}
	permission, e := x.projectPermission(ctx, projectID, u)
	if e != nil {
		http.Error(w, "Нет доступа", 403)
		return
	}
	filters, err := parseLedgerFilters(r.URL.Query().Get("system"), r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, "Некорректный фильтр", http.StatusBadRequest)
		return
	}
	data := ledgerView{FilterSystem: filters.System, FilterQuery: filters.Query, RevisionID: id, ProjectID: projectID, ProjectName: name, Status: status, CSRF: x.csrf(r, u), CanWrite: canEdit(permission) && status == "draft", Total: "0.00"}
	rows, e := x.db.Query(ctx, `SELECT id,code,name FROM project_systems WHERE revision_id=$1 ORDER BY sort_order,code`, id)
	if e != nil {
		http.Error(w, "Ошибка систем", 500)
		return
	}
	for rows.Next() {
		var s ledgerSystem
		if e = rows.Scan(&s.ID, &s.Code, &s.Name); e != nil {
			break
		}
		s.Total = "0.00"
		data.Systems = append(data.Systems, s)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка систем", 500)
		return
	}
	rows, e = x.db.Query(ctx, `SELECT id,name FROM project_locations WHERE revision_id=$1 ORDER BY name`, id)
	if e != nil {
		http.Error(w, "Ошибка мест", 500)
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
		http.Error(w, "Ошибка мест", 500)
		return
	}
	rows, e = x.db.Query(ctx, `SELECT i.id::text,i.system_id::text,i.name_snapshot,i.input_quantity::text,i.input_unit_code,COALESCE(i.coefficient::text,''),i.calculated_quantity::text,i.billable_unit_code,i.unit_price::text,i.amount::text,COALESCE(l.name,''),COALESCE(i.location_id::text,''),COALESCE(i.rule_snapshot->>'mode','direct'),COALESCE(i.inputs_snapshot->>'width_mm',''),COALESCE(i.inputs_snapshot->>'height_mm',''),COALESCE(i.inputs_snapshot->>'diameter_mm',''),COALESCE(i.inputs_snapshot->>'source_item_id',''),COALESCE((SELECT string_agg(z,',') FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(i.inputs_snapshot->'include_ids')='array' THEN i.inputs_snapshot->'include_ids' ELSE '[]'::jsonb END) z),'') ,COALESCE((SELECT string_agg(z,',') FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(i.inputs_snapshot->'exclude_ids')='array' THEN i.inputs_snapshot->'exclude_ids' ELSE '[]'::jsonb END) z),'') FROM project_work_items i LEFT JOIN project_locations l ON l.id=i.location_id AND l.revision_id=i.revision_id WHERE i.revision_id=$1 ORDER BY i.sort_order,i.id`, id)
	if e != nil {
		http.Error(w, "Ошибка работ", 500)
		return
	}
	// Database aggregation avoids converting money to float64.
	sums := make(map[string]string)
	for rows.Next() {
		var item ledgerItem
		var systemID string
		if e = rows.Scan(&item.ID, &systemID, &item.Name, &item.Quantity, &item.InputUnit, &item.Coefficient, &item.Calculated, &item.ResultUnit, &item.UnitPrice, &item.Amount, &item.Location, &item.LocationID, &item.Mode, &item.WidthMM, &item.HeightMM, &item.DiameterMM, &item.SourceID, &item.IncludeCSV, &item.ExcludeCSV); e != nil {
			break
		}
		for j := range data.Systems {
			if data.Systems[j].ID == systemID {
				data.Systems[j].Items = append(data.Systems[j].Items, item)
				data.AllItems = append(data.AllItems, item)
				break
			}
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка чтения строк", 500)
		return
	}
	rows, e = x.db.Query(ctx, `SELECT system_id::text,COALESCE(SUM(amount),0)::text FROM project_work_items WHERE revision_id=$1 GROUP BY system_id`, id)
	if e != nil {
		http.Error(w, "Ошибка итогов", 500)
		return
	}
	for rows.Next() {
		var systemID, total string
		if e = rows.Scan(&systemID, &total); e != nil {
			break
		}
		sums[systemID] = total
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		http.Error(w, "Ошибка итогов", 500)
		return
	}
	for j := range data.Systems {
		if s, ok := sums[data.Systems[j].ID]; ok {
			data.Systems[j].Total = s
		}
	}
	e = x.db.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0)::text FROM project_work_items WHERE revision_id=$1`, id).Scan(&data.Total)
	if e != nil {
		http.Error(w, "Ошибка суммы", 500)
		return
	}
	data.TotalCount = len(data.AllItems)
	for j := range data.Systems {
		sys := &data.Systems[j]
		sys.Visible = filters.System == "" || filters.System == sys.ID
		if !sys.Visible {
			continue
		}
		if filters.Query != "" {
			kept := make([]ledgerItem, 0, len(sys.Items))
			for _, item := range sys.Items {
				if ledgerMatches(item.Name, item.Location, filters.Query) {
					kept = append(kept, item)
				}
			}
			sys.Items = kept
		}
		data.VisibleCount += len(sys.Items)
	}
	ledgerRender(w, 200, data)
}

type itemInput struct {
	Name, SystemID, LocationID, Quantity, Unit, Mode, Coefficient, Price, WidthMM, HeightMM, DiameterMM, SourceID string
	IncludeIDs, ExcludeIDs                                                                                        []string
}

func parseItemInput(r *http.Request) itemInput {
	_ = r.ParseForm()
	return itemInput{IncludeIDs: r.PostForm["include_ids"], ExcludeIDs: r.PostForm["exclude_ids"], Name: strings.TrimSpace(r.PostFormValue("name")), SystemID: r.PostFormValue("system_id"), LocationID: r.PostFormValue("location_id"), Quantity: r.PostFormValue("quantity"), Unit: r.PostFormValue("unit"), Mode: r.PostFormValue("mode"), Coefficient: r.PostFormValue("coefficient"), Price: r.PostFormValue("price"), WidthMM: r.PostFormValue("width_mm"), HeightMM: r.PostFormValue("height_mm"), DiameterMM: r.PostFormValue("diameter_mm"), SourceID: r.PostFormValue("source_item_id")}
}
func validateItem(i itemInput) (calculation.Result, string, error) {
	if strings.TrimSpace(i.Name) == "" || len(i.Name) > 250 {
		return calculation.Result{}, "", errors.New("Некорректное название")
	}
	if i.Unit != "m" && i.Unit != "pcs" && i.Unit != "m2" {
		return calculation.Result{}, "", errors.New("Некорректная единица")
	}
	if (i.Mode == "coefficient" || i.Mode == "reference_coefficient") && i.Unit == "m2" {
		return calculation.Result{}, "", errors.New("Для м² выберите прямой расчёт")
	}
	if i.LocationID != "" && !uuidPattern.MatchString(i.LocationID) {
		return calculation.Result{}, "", errors.New("Некорректное место")
	}
	var result calculation.Result
	var e error
	switch i.Mode {
	case "direct", "coefficient":
		result, e = calculation.Compute(i.Mode, i.Quantity, i.Coefficient, i.Price)
	case "reference_coefficient":
		if i.Unit != "pcs" {
			return calculation.Result{}, "", errors.New("Справочный коэффициент применяется к шт.")
		}
		result, e = calculation.Compute("coefficient", i.Quantity, i.Coefficient, i.Price)
	case "geometry_rectangular":
		if i.Unit != "m" {
			return calculation.Result{}, "", errors.New("Для геометрии единица должна быть пог. м")
		}
		result, e = calculation.Geometry(i.Mode, i.Quantity, i.WidthMM, i.HeightMM, i.Price)
	case "geometry_round":
		if i.Unit != "m" {
			return calculation.Result{}, "", errors.New("Для геометрии единица должна быть пог. м")
		}
		result, e = calculation.Geometry(i.Mode, i.Quantity, i.DiameterMM, "", i.Price)
	case "sum_multi":
		if i.Unit != "m2" || len(i.IncludeIDs) == 0 || len(i.IncludeIDs) > 100 || len(i.ExcludeIDs) > 100 {
			return calculation.Result{}, "", errors.New("Выберите исходные площади (м²), максимум 100")
		}
		seen := map[string]bool{}
		for _, id := range append(append([]string{}, i.IncludeIDs...), i.ExcludeIDs...) {
			if !uuidPattern.MatchString(id) || seen[id] {
				return calculation.Result{}, "", errors.New("Дублирующиеся или некорректные источники")
			}
			seen[id] = true
		}
		if _, e = calculation.Parse(i.Coefficient, 10); e != nil {
			return calculation.Result{}, "", e
		}
		if _, e = calculation.Parse(i.Price, 4); e != nil {
			return calculation.Result{}, "", e
		}
		if _, e = calculation.Parse(i.Quantity, 8); e != nil {
			return calculation.Result{}, "", e
		}
		result = calculation.Result{}
	case "sum_items":
		if i.Unit != "m2" || !uuidPattern.MatchString(i.SourceID) {
			return calculation.Result{}, "", errors.New("Для изоляции выберите единицу м² и строку-источник")
		}
		if _, e = calculation.Parse(i.Coefficient, 10); e != nil {
			return calculation.Result{}, "", e
		}
		if _, e = calculation.Parse(i.Price, 4); e != nil {
			return calculation.Result{}, "", e
		}
		result = calculation.Result{}
	default:
		return calculation.Result{}, "", errors.New("Неизвестный режим расчёта")
	}
	if e != nil {
		return result, "", e
	}
	billed := i.Unit
	if i.Mode != "direct" {
		billed = "m2"
	}
	return result, billed, nil
}
func (x web) itemSave(w http.ResponseWriter, r *http.Request, u auth.Identity, update bool) {
	id, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	var itemID string
	if update {
		itemID = r.PathValue("item")
		if !uuidPattern.MatchString(itemID) {
			http.Error(w, "Некорректная позиция", 400)
			return
		}
	}
	input := parseItemInput(r)
	if !update && !uuidPattern.MatchString(input.SystemID) {
		http.Error(w, "Некорректная система", 400)
		return
	}
	result, billed, e := validateItem(input)
	if e != nil {
		http.Error(w, "Некорректная строка: "+e.Error(), 400)
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
	// Serialize edits and approval against the parent revision; SQL trigger also protects approved rows.
	var status string
	e = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 FOR UPDATE`, id).Scan(&status)
	if e != nil || status != "draft" {
		http.Error(w, "Ревизия закрыта", 409)
		return
	}
	// The revision row is locked before reading any referenced quantities.
	if input.Mode == "sum_items" {
		if input.SourceID == itemID {
			http.Error(w, "Строка не может ссылаться на себя", 400)
			return
		}
		var sourceArea, sourceUnit string
		e = tx.QueryRow(ctx, `SELECT calculated_quantity::text,result_unit_code FROM project_work_items WHERE revision_id=$1 AND id=$2`, id, input.SourceID).Scan(&sourceArea, &sourceUnit)
		if e != nil || sourceUnit != "m2" {
			http.Error(w, "Источник отсутствует или не рассчитан в м²", 400)
			return
		}
		result, e = calculation.Linked(sourceArea, input.Coefficient, input.Price)
		if e != nil {
			http.Error(w, "Некорректный расчёт изоляции", 400)
			return
		}
	} else if input.Mode == "sum_multi" {
		included := make([]string, 0, len(input.IncludeIDs))
		excluded := make([]string, 0, len(input.ExcludeIDs))
		for _, sid := range append(append([]string{}, input.IncludeIDs...), input.ExcludeIDs...) {
			if sid == itemID {
				http.Error(w, "Самоссылка запрещена", 400)
				return
			}
			var area, unit string
			e = tx.QueryRow(ctx, `SELECT calculated_quantity::text,result_unit_code FROM project_work_items WHERE revision_id=$1 AND id=$2`, id, sid).Scan(&area, &unit)
			if e != nil || unit != "m2" {
				http.Error(w, "Источник отсутствует в ревизии или не имеет единицу м²", 400)
				return
			}
			if len(included) < len(input.IncludeIDs) {
				included = append(included, area)
			} else {
				excluded = append(excluded, area)
			}
		}
		result, e = calculation.AggregateArea(included, excluded, input.Coefficient, input.Price)
		if e != nil {
			http.Error(w, "Некорректные площади: "+e.Error(), 400)
			return
		}
	}
	if input.IncludeIDs == nil {
		input.IncludeIDs = []string{}
	}
	if input.ExcludeIDs == nil {
		input.ExcludeIDs = []string{}
	}
	snapshotSource := ""
	if input.Mode == "sum_items" {
		snapshotSource = input.SourceID
	}
	snapshot, _ := json.Marshal(map[string]any{"width_mm": resultInput(input.WidthMM), "height_mm": resultInput(input.HeightMM), "diameter_mm": resultInput(input.DiameterMM), "source_item_id": snapshotSource, "include_ids": input.IncludeIDs, "exclude_ids": input.ExcludeIDs})

	var location any
	if input.LocationID != "" {
		location = input.LocationID
	}
	var affected int64
	if update {
		tag, execErr := tx.Exec(ctx, `UPDATE project_work_items SET name_snapshot=$3,input_quantity=$4,input_unit_code=$5,coefficient=$6,calculated_quantity=$7,result_unit_code=$8,billable_quantity=$9,billable_unit_code=$8,unit_price=$10,amount=$11,location_id=$12,rule_snapshot=jsonb_build_object('mode',$13::text,'rule_version',2),inputs_snapshot=$14::jsonb,manually_modified=true WHERE revision_id=$1 AND id=$2 AND ($12::uuid IS NULL OR EXISTS(SELECT 1 FROM project_locations l WHERE l.id=$12 AND l.revision_id=$1))`, id, itemID, input.Name, resultInput(input.Quantity), input.Unit, nullableCoefficient(result.Coefficient), result.Calculated, billed, result.Billable, resultInput(input.Price), result.Amount, location, input.Mode, string(snapshot))
		e = execErr
		if e == nil {
			affected = tag.RowsAffected()
		}
	} else {
		if input.SystemID == "" {
			http.Error(w, "Выберите систему", 400)
			return
		}
		tag, execErr := tx.Exec(ctx, `INSERT INTO project_work_items(revision_id,system_id,location_id,name_snapshot,input_quantity,input_unit_code,coefficient,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount,rule_snapshot,inputs_snapshot,manually_modified,sort_order) SELECT $1,s.id,$3,$4,$5,$6,$7,$8,$9,$10,$9,$11,$12,jsonb_build_object('mode',$13::text,'rule_version',2),$14::jsonb,true,(SELECT COALESCE(MAX(sort_order),0)+1 FROM project_work_items WHERE revision_id=$1) FROM project_systems s WHERE s.revision_id=$1 AND s.id=$2 AND ($3::uuid IS NULL OR EXISTS(SELECT 1 FROM project_locations l WHERE l.id=$3 AND l.revision_id=$1))`, id, input.SystemID, location, input.Name, resultInput(input.Quantity), input.Unit, nullableCoefficient(result.Coefficient), result.Calculated, billed, result.Billable, resultInput(input.Price), result.Amount, input.Mode, string(snapshot))
		e = execErr
		if e == nil {
			affected = tag.RowsAffected()
		}
	}
	if e != nil {
		slog.Error("ledger save query failed", "error", e, "revision_id", id, "update", update)
		http.Error(w, "Ошибка сохранения строки", 500)
		return
	}
	if affected != 1 {
		http.Error(w, "Система, место или строка не найдены", 404)
		return
	}
	if e = refreshDependents(ctx, tx, id); e != nil {
		slog.Error("dependent calculation failed", "error", e, "revision_id", id)
		http.Error(w, "Ошибка пересчёта зависимых работ", 409)
		return
	}
	if e = tx.Commit(ctx); e != nil {
		http.Error(w, "Ошибка фиксации", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+id+"/ledger", http.StatusSeeOther)
}
func resultInput(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), ",", ".") }
func nullableCoefficient(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (x web) itemCreate(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.itemSave(w, r, u, false)
}
func (x web) itemUpdate(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.itemSave(w, r, u, true)
}
func (x web) itemDelete(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	itemID := r.PathValue("item")
	if !uuidPattern.MatchString(itemID) {
		http.Error(w, "Некорректная позиция", 400)
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
	var status string
	e = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 FOR UPDATE`, id).Scan(&status)
	if e != nil || status != "draft" {
		http.Error(w, "Ревизия закрыта", 409)
		return
	}
	var linkedCount int
	e = tx.QueryRow(ctx, `SELECT COUNT(*) FROM project_work_items WHERE revision_id=$1 AND (inputs_snapshot->>'source_item_id'=$2 OR COALESCE(inputs_snapshot->'include_ids','[]'::jsonb) ? $2 OR COALESCE(inputs_snapshot->'exclude_ids','[]'::jsonb) ? $2)`, id, itemID).Scan(&linkedCount)
	if e != nil {
		http.Error(w, "Ошибка проверки зависимостей", 500)
		return
	}
	if linkedCount > 0 {
		http.Error(w, "Сначала удалите зависимые работы", 409)
		return
	}
	tag, e := tx.Exec(ctx, `DELETE FROM project_work_items WHERE revision_id=$1 AND id=$2`, id, itemID)
	if e != nil {
		http.Error(w, "Нельзя удалить назначенную или закрытую работу", 409)
		return
	}
	if tag.RowsAffected() != 1 {
		http.Error(w, "Позиция не найдена", 404)
		return
	}
	if e = tx.Commit(ctx); e != nil {
		http.Error(w, "Ошибка сохранения", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+id+"/ledger", http.StatusSeeOther)
}

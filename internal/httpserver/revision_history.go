package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"montage-ledger/internal/auth"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
)

// lineage_id gives a copied row a stable identity across revisions. It is not a
// natural key: independently created revisions cannot reliably be matched.
func newRevisionUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Remap only string values that are exact identifiers; do not touch names,
// arbitrary numeric quantities, or embedded substrings.
func remapSnapshot(raw []byte, ids map[string]string) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	var visit func(any) any
	visit = func(v any) any {
		switch t := v.(type) {
		case string:
			if replacement, ok := ids[t]; ok {
				return replacement
			}
			return t
		case []any:
			for i := range t {
				t[i] = visit(t[i])
			}
			return t
		case map[string]any:
			for k, x := range t {
				t[k] = visit(x)
			}
			return t
		default:
			return v
		}
	}
	return json.Marshal(visit(value))
}

type copySystem struct {
	old, code, name, kind string
	order                 int
}
type copyLocation struct{ old, name string }
type copyItem struct {
	old, system, location, lineage string
	rule, inputs, trace            []byte
}

func (x web) revisionClone(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	source := paramID(w, r)
	if source == "" {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	project, status, _, err := x.revisionAccess(ctx, source, u)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = status
	permission, err := x.projectPermission(ctx, project, u)
	if err != nil || !canEdit(permission) {
		http.Error(w, "Недостаточно прав", 403)
		return
	}
	tx, err := x.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		http.Error(w, "Ошибка транзакции", 500)
		return
	}
	defer tx.Rollback(ctx)
	// Serializes version allocation and conflicts with changes in source content.
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM projects WHERE id=$1 FOR UPDATE`, project).Scan(&locked); err != nil {
		http.Error(w, "Объект не найден", 404)
		return
	}
	if err = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 AND project_id=$2 FOR SHARE`, source, project).Scan(&status); err != nil {
		http.Error(w, "Ревизия недоступна", 404)
		return
	}
	var dest string
	if err = tx.QueryRow(ctx, `INSERT INTO project_revisions(project_id,number,created_by) SELECT $1,COALESCE(MAX(number),0)+1,$2 FROM project_revisions WHERE project_id=$1 RETURNING id::text`, project, u.ID).Scan(&dest); err != nil {
		http.Error(w, "Не удалось создать ревизию", 500)
		return
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, u.ID); err != nil {
		http.Error(w, "Ошибка аудита", 500)
		return
	}
	ids := map[string]string{}
	systems := []copySystem{}
	rows, err := tx.Query(ctx, `SELECT id::text,code,name,type_code,sort_order FROM project_systems WHERE revision_id=$1 ORDER BY sort_order,id`, source)
	if err != nil {
		http.Error(w, "Ошибка систем", 500)
		return
	}
	for rows.Next() {
		var v copySystem
		if err = rows.Scan(&v.old, &v.code, &v.name, &v.kind, &v.order); err != nil {
			break
		}
		systems = append(systems, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка систем", 500)
		return
	}
	for _, s := range systems {
		id, e := newRevisionUUID()
		if e != nil {
			http.Error(w, "Ошибка UUID", 500)
			return
		}
		ids[s.old] = id
		if _, e = tx.Exec(ctx, `INSERT INTO project_systems(id,revision_id,code,name,type_code,sort_order) VALUES ($1,$2,$3,$4,$5,$6)`, id, dest, s.code, s.name, s.kind, s.order); e != nil {
			http.Error(w, "Ошибка копирования систем", 500)
			return
		}
	}
	locations := []copyLocation{}
	rows, err = tx.Query(ctx, `SELECT id::text,name FROM project_locations WHERE revision_id=$1 ORDER BY id`, source)
	if err != nil {
		http.Error(w, "Ошибка помещений", 500)
		return
	}
	for rows.Next() {
		var v copyLocation
		if err = rows.Scan(&v.old, &v.name); err != nil {
			break
		}
		locations = append(locations, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка помещений", 500)
		return
	}
	for _, l := range locations {
		id, e := newRevisionUUID()
		if e != nil {
			http.Error(w, "Ошибка UUID", 500)
			return
		}
		ids[l.old] = id
		if _, e = tx.Exec(ctx, `INSERT INTO project_locations(id,revision_id,name) VALUES ($1,$2,$3)`, id, dest, l.name); e != nil {
			http.Error(w, "Ошибка копирования помещений", 500)
			return
		}
	}
	items := []copyItem{}
	rows, err = tx.Query(ctx, `SELECT id::text,system_id::text,COALESCE(location_id::text,''),lineage_id::text,rule_snapshot,inputs_snapshot,trace_snapshot FROM project_work_items WHERE revision_id=$1 ORDER BY sort_order,id`, source)
	if err != nil {
		http.Error(w, "Ошибка строк", 500)
		return
	}
	for rows.Next() {
		var it copyItem
		if err = rows.Scan(&it.old, &it.system, &it.location, &it.lineage, &it.rule, &it.inputs, &it.trace); err != nil {
			break
		}
		items = append(items, it)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка строк", 500)
		return
	}
	for _, it := range items {
		id, e := newRevisionUUID()
		if e != nil {
			http.Error(w, "Ошибка UUID", 500)
			return
		}
		ids[it.old] = id
	}
	for _, it := range items {
		rule, e := remapSnapshot(it.rule, ids)
		if e != nil {
			http.Error(w, "Ошибка снимка правила", 500)
			return
		}
		inputs, e := remapSnapshot(it.inputs, ids)
		if e != nil {
			http.Error(w, "Ошибка зависимостей", 500)
			return
		}
		trace, e := remapSnapshot(it.trace, ids)
		if e != nil {
			http.Error(w, "Ошибка трассировки", 500)
			return
		}
		var location any
		if it.location != "" {
			mapped, ok := ids[it.location]
			if !ok {
				http.Error(w, "Неверная ссылка на место", 409)
				return
			}
			location = mapped
		}
		system, ok := ids[it.system]
		if !ok {
			http.Error(w, "Неверная ссылка на систему", 409)
			return
		}
		_, e = tx.Exec(ctx, `INSERT INTO project_work_items(id,revision_id,system_id,location_id,work_catalog_id,name_snapshot,source_type,input_quantity,input_unit_code,coefficient,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount,currency_code,rule_snapshot,inputs_snapshot,trace_snapshot,manually_modified,notes,sort_order,lineage_id)
 SELECT $3,$4,$5,$6,work_catalog_id,name_snapshot,source_type,input_quantity,input_unit_code,coefficient,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount,currency_code,$7::jsonb,$8::jsonb,$9::jsonb,manually_modified,notes,sort_order,$10::uuid FROM project_work_items WHERE revision_id=$1 AND id=$2`, source, it.old, ids[it.old], dest, system, location, string(rule), string(inputs), string(trace), it.lineage)
		if e != nil {
			slog.Error("clone work item failed", "error", e)
			http.Error(w, "Ошибка копирования позиции", 500)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		http.Error(w, "Ошибка фиксации новой ревизии", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+dest, http.StatusSeeOther)
}

type revisionDifference struct{ Code, Name, Change, BeforeQuantity, AfterQuantity, BeforePrice, AfterPrice, BeforeAmount, AfterAmount string }
type revisionAuditRecord struct{ ID, At, Actor, Action, Item, Before, After string }
type revisionDiffView struct {
	Source, Target, Project, CSRF string
	SourceNumber, TargetNumber    int
	CanClone                      bool
	Rows                          []revisionDifference
	Systems                       []revisionSystemDelta
	Audit                         []revisionAuditRecord
}

const revisionDiffHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Сравнение ревизий</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:1300px;margin:32px auto;padding:16px"><h1>Сравнение ревизий №{{.SourceNumber}} и №{{.TargetNumber}}</h1><p><a href="/app/revisions/{{.Target}}">К ревизии</a> · <a href="/app/revisions/{{.Target}}/compare?against={{.Source}}&export=1">Экспорт сравнения XLSX</a></p><p>Строки сопоставляются по идентификатору происхождения. Независимо созданные строки отображаются как добавленные или удалённые. Суммы из исторических снимков не пересчитываются.</p><h2>Итоги по системам</h2><div style="overflow:auto"><table><thead><tr><th>Система</th><th>До, ₽</th><th>После, ₽</th><th>Разница, ₽</th></tr></thead><tbody>{{range .Systems}}<tr><td>{{.Code}}</td><td>{{.Before}}</td><td>{{.After}}</td><td>{{.Difference}}</td></tr>{{end}}</tbody></table></div><h2>Различия позиций</h2><div style="overflow:auto"><table><thead><tr><th>Система</th><th>Работа</th><th>Изменение</th><th>Количество до</th><th>После</th><th>Цена до</th><th>После</th><th>Сумма до</th><th>После</th></tr></thead><tbody>{{range .Rows}}<tr><td>{{.Code}}</td><td>{{.Name}}</td><td>{{.Change}}</td><td>{{.BeforeQuantity}}</td><td>{{.AfterQuantity}}</td><td>{{.BeforePrice}}</td><td>{{.AfterPrice}}</td><td>{{.BeforeAmount}}</td><td>{{.AfterAmount}}</td></tr>{{else}}<tr><td colspan="9">Различий нет</td></tr>{{end}}</tbody></table></div><h2>Журнал изменений позиций (последние 100)</h2><div style="overflow:auto"><table><thead><tr><th>Когда</th><th>Автор</th><th>Действие</th><th>Работа</th><th>Было</th><th>Стало</th></tr></thead><tbody>{{range .Audit}}<tr><td>{{.At}}</td><td>{{.Actor}}</td><td>{{.Action}}</td><td>{{.Item}}</td><td>{{.Before}}</td><td>{{.After}}</td></tr>{{else}}<tr><td colspan="6">История пока пуста</td></tr>{{end}}</tbody></table></div></main></body></html>`

var revisionDiffTemplate = template.Must(template.New("revisionDiff").Parse(revisionDiffHTML))

func (x web) loadRevisionDiff(ctx context.Context, source, target string) ([]revisionDifference, error) {
	// lineage_id is stable for copies, but separate revision rows may be unrelated.
	rows, err := x.db.Query(ctx, `WITH a AS (
 SELECT i.lineage_id, i.name_snapshot, s.code, i.input_quantity::text quantity,i.unit_price::text price,i.amount::text amount,
 to_jsonb(i) - 'id' - 'revision_id' - 'system_id' - 'location_id' - 'inputs_snapshot' - 'trace_snapshot' - 'lineage_id' - 'rule_snapshot' body,
 canonical_revision_inputs(i.inputs_snapshot,$1) inputs, i.rule_snapshot rules, i.trace_snapshot trace,
 (SELECT l.name FROM project_locations l WHERE l.id=i.location_id) location
 FROM project_work_items i JOIN project_systems s ON s.id=i.system_id WHERE i.revision_id=$1),
 b AS (
 SELECT i.lineage_id, i.name_snapshot,s.code,i.input_quantity::text quantity,i.unit_price::text price,i.amount::text amount,
 to_jsonb(i) - 'id' - 'revision_id' - 'system_id' - 'location_id' - 'inputs_snapshot' - 'trace_snapshot' - 'lineage_id' - 'rule_snapshot' body,
 canonical_revision_inputs(i.inputs_snapshot,$2) inputs, i.rule_snapshot rules, i.trace_snapshot trace,
 (SELECT l.name FROM project_locations l WHERE l.id=i.location_id) location
 FROM project_work_items i JOIN project_systems s ON s.id=i.system_id WHERE i.revision_id=$2)
 SELECT COALESCE(b.code,a.code),COALESCE(b.name_snapshot,a.name_snapshot),
 CASE WHEN a.lineage_id IS NULL THEN 'Добавлена' WHEN b.lineage_id IS NULL THEN 'Удалена' ELSE 'Изменена' END,
 COALESCE(a.quantity,''),COALESCE(b.quantity,''),COALESCE(a.price,''),COALESCE(b.price,''),COALESCE(a.amount,''),COALESCE(b.amount,'')
 FROM a FULL JOIN b USING(lineage_id)
 WHERE a.lineage_id IS NULL OR b.lineage_id IS NULL OR a.body IS DISTINCT FROM b.body OR a.code IS DISTINCT FROM b.code OR a.location IS DISTINCT FROM b.location OR a.inputs IS DISTINCT FROM b.inputs OR a.rules IS DISTINCT FROM b.rules OR a.trace IS DISTINCT FROM b.trace
 ORDER BY 1,2,3`, source, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []revisionDifference{}
	for rows.Next() {
		var v revisionDifference
		if err = rows.Scan(&v.Code, &v.Name, &v.Change, &v.BeforeQuantity, &v.AfterQuantity, &v.BeforePrice, &v.AfterPrice, &v.BeforeAmount, &v.AfterAmount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (x web) revisionCompare(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	target := paramID(w, r)
	if target == "" {
		return
	}
	source := strings.TrimSpace(r.URL.Query().Get("against"))
	if !uuidPattern.MatchString(source) || source == target {
		http.Error(w, "Выберите другую ревизию в параметре against", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	project, _, _, err := x.revisionAccess(ctx, target, u)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	other, _, _, err := x.revisionAccess(ctx, source, u)
	if err != nil || other != project {
		http.Error(w, "Ревизии должны относиться к одному доступному объекту", 403)
		return
	}
	d := revisionDiffView{Source: source, Target: target, Project: project}
	if err = x.db.QueryRow(ctx, `SELECT (SELECT number FROM project_revisions WHERE id=$1),(SELECT number FROM project_revisions WHERE id=$2)`, source, target).Scan(&d.SourceNumber, &d.TargetNumber); err != nil {
		http.Error(w, "Ошибка номеров", 500)
		return
	}
	d.Rows, err = x.loadRevisionDiff(ctx, source, target)
	if err != nil {
		slog.Error("revision diff failed", "error", err)
		http.Error(w, "Ошибка сравнения", 500)
		return
	}
	d.Systems, err = x.loadRevisionSystemDeltas(ctx, source, target)
	if err != nil {
		slog.Error("revision system sums failed", "error", err)
		http.Error(w, "Ошибка сумм систем", 500)
		return
	}
	if r.URL.Query().Get("export") == "1" {
		exportRevisionDiff(w, d)
		return
	}
	rs, err := x.db.Query(ctx, `SELECT e.id::text,e.happened_at,COALESCE(u.display_name,'Не определён'),e.action,COALESCE(e.after_row->>'name_snapshot',e.before_row->>'name_snapshot',''),COALESCE(e.before_row->>'amount',''),COALESCE(e.after_row->>'amount','') FROM work_item_change_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.revision_id=$1 ORDER BY e.id DESC LIMIT 100`, target)
	if err != nil {
		http.Error(w, "Ошибка истории", 500)
		return
	}
	for rs.Next() {
		var a revisionAuditRecord
		var happened time.Time
		if err = rs.Scan(&a.ID, &happened, &a.Actor, &a.Action, &a.Item, &a.Before, &a.After); err != nil {
			break
		}
		a.At = happened.UTC().Format("2006-01-02 15:04:05")
		d.Audit = append(d.Audit, a)
	}
	if err == nil {
		err = rs.Err()
	}
	rs.Close()
	if err != nil {
		http.Error(w, "Ошибка истории", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if err = revisionDiffTemplate.Execute(w, d); err != nil {
		slog.Error("revision diff render", "error", err)
	}
}

func exportRevisionDiff(w http.ResponseWriter, d revisionDiffView) {
	f := excelize.NewFile()
	sheet := "Сравнение"
	f.SetSheetName("Sheet1", sheet)
	headers := []string{"Система", "Работа", "Изменение", "Количество до", "Количество после", "Цена до", "Цена после", "Сумма до", "Сумма после"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellStr(sheet, cell, h)
	}
	for row, it := range d.Rows {
		values := []string{it.Code, it.Name, it.Change, it.BeforeQuantity, it.AfterQuantity, it.BeforePrice, it.AfterPrice, it.BeforeAmount, it.AfterAmount}
		for col, val := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
			_ = f.SetCellStr(sheet, cell, val)
		}
	}
	totalsSheet := "Итоги систем"
	f.NewSheet(totalsSheet)
	for col, title := range []string{"Система", "До, ₽", "После, ₽", "Разница, ₽"} {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		_ = f.SetCellStr(totalsSheet, cell, title)
	}
	for index, v := range d.Systems {
		for col, value := range []string{v.Code, v.Before, v.After, v.Difference} {
			cell, _ := excelize.CoordinatesToCellName(col+1, index+2)
			_ = f.SetCellStr(totalsSheet, cell, value)
		}
	}
	f.SetColWidth(totalsSheet, "A", "A", 18)
	f.SetColWidth(sheet, "A", "A", 13)
	f.SetColWidth(sheet, "B", "B", 54)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="revision-%d-vs-%d.xlsx"`, d.SourceNumber, d.TargetNumber))
	w.Header().Set("Cache-Control", "no-store")
	if err := f.Write(w); err != nil {
		slog.Error("revision XLSX export failed", "error", err)
	}
}

// compareTargets lists revision numbers for a safe, same-project comparison UI.
func (x web) compareTargets(ctx context.Context, project, target string) ([]struct {
	ID     string
	Number int
}, error) {
	rs, err := x.db.Query(ctx, `SELECT id::text,number FROM project_revisions WHERE project_id=$1 AND id<>$2 ORDER BY number DESC`, project, target)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []struct {
		ID     string
		Number int
	}{}
	for rs.Next() {
		var v struct {
			ID     string
			Number int
		}
		if err = rs.Scan(&v.ID, &v.Number); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rs.Err()
}

const itemHistoryHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Журнал строк ведомости</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:1150px;margin:32px auto;padding:16px"><h1>История изменений строк</h1><p><a href="/app/revisions/{{.RevisionID}}">К ревизии</a></p><p>Записи созданы после установки Stage 12.5. У прямых SQL-изменений автор может быть не определён.</p><div style="overflow:auto"><table><thead><tr><th>Время UTC</th><th>Автор</th><th>Действие</th><th>Работа</th><th>Сумма до</th><th>Сумма после</th></tr></thead><tbody>{{range .Records}}<tr><td>{{.At}}</td><td>{{.Actor}}</td><td>{{.Action}}</td><td>{{.Item}}</td><td>{{.Before}}</td><td>{{.After}}</td></tr>{{else}}<tr><td colspan="6">Записей нет</td></tr>{{end}}</tbody></table></div></main></body></html>`

var itemHistoryTemplate = template.Must(template.New("itemHistory").Parse(itemHistoryHTML))

type itemHistoryView struct {
	RevisionID string
	Records    []revisionAuditRecord
}

func (x web) revisionHistory(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id := paramID(w, r)
	if id == "" {
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	if _, _, _, err := x.revisionAccess(ctx, id, u); err != nil {
		http.NotFound(w, r)
		return
	}
	rs, err := x.db.Query(ctx, `SELECT e.id::text,e.happened_at,COALESCE(u.display_name,'Не определён'),e.action,COALESCE(e.after_row->>'name_snapshot',e.before_row->>'name_snapshot',''),COALESCE(e.before_row->>'amount',''),COALESCE(e.after_row->>'amount','') FROM work_item_change_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.revision_id=$1 ORDER BY e.id DESC LIMIT 500`, id)
	if err != nil {
		slog.Error("work history query failed", "error", err)
		http.Error(w, "Ошибка истории", 500)
		return
	}
	data := itemHistoryView{RevisionID: id}
	for rs.Next() {
		var a revisionAuditRecord
		var at time.Time
		if err = rs.Scan(&a.ID, &at, &a.Actor, &a.Action, &a.Item, &a.Before, &a.After); err != nil {
			break
		}
		a.At = at.UTC().Format("2006-01-02 15:04:05")
		data.Records = append(data.Records, a)
	}
	if err == nil {
		err = rs.Err()
	}
	rs.Close()
	if err != nil {
		http.Error(w, "Ошибка истории", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if err = itemHistoryTemplate.Execute(w, data); err != nil {
		slog.Error("history render failed", "error", err)
	}
}

// System summaries use PostgreSQL NUMERIC aggregation and preserve source snapshots.
func (x web) loadRevisionSystemDeltas(ctx context.Context, source, target string) ([]revisionSystemDelta, error) {
	rs, err := x.db.Query(ctx, `WITH a AS (
 SELECT s.code,COALESCE(SUM(i.amount),0) total FROM project_systems s
 LEFT JOIN project_work_items i ON i.system_id=s.id AND i.revision_id=s.revision_id
 WHERE s.revision_id=$1 GROUP BY s.code),
 b AS (
 SELECT s.code,COALESCE(SUM(i.amount),0) total FROM project_systems s
 LEFT JOIN project_work_items i ON i.system_id=s.id AND i.revision_id=s.revision_id
 WHERE s.revision_id=$2 GROUP BY s.code)
 SELECT COALESCE(a.code,b.code),COALESCE(a.total,0)::numeric(20,2)::text,
 COALESCE(b.total,0)::numeric(20,2)::text,
 (COALESCE(b.total,0)-COALESCE(a.total,0))::numeric(20,2)::text
 FROM a FULL JOIN b USING(code) ORDER BY 1`, source, target)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	data := []revisionSystemDelta{}
	for rs.Next() {
		var s revisionSystemDelta
		if err = rs.Scan(&s.Code, &s.Before, &s.After, &s.Difference); err != nil {
			return nil, err
		}
		data = append(data, s)
	}
	return data, rs.Err()
}

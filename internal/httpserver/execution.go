package httpserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"montage-ledger/internal/auth"
	"montage-ledger/internal/calculation"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
)

type executionItem struct{ ID, Name, Unit, Quantity, Recorded string }
type executionWorker struct{ ID, Name string }
type executionAssignment struct{ ID, Name, Worker, Unit, Rate, Recorded, Approved, Accrued string }
type executionEvent struct {
	ID, Worker, Work, Quantity, Date, Note, Status string
	CanApprove                                     bool
}
type executionData struct {
	RevisionID, ProjectName, Status, CSRF string
	CanWrite, CanApprove                  bool
	Items                                 []executionItem
	Workers                               []executionWorker
	Assignments                           []executionAssignment
	Events                                []executionEvent
	TotalApproved                         string
}

const executionHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Факт монтажа и начисления</title><style>body{font:15px/1.5 system-ui,sans-serif;background:#f5f7fa;color:#182236;margin:0}header{background:#182743;color:white;padding:17px 5%}header a{color:white}main{max-width:1250px;margin:24px auto;padding:0 18px}section{background:white;border:1px solid #dae2ec;border-radius:10px;padding:20px;margin:18px 0;overflow:auto}h1{margin-bottom:0}h2{font-size:19px}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #e4e8ef;padding:9px;text-align:left}input,select{padding:9px;border:1px solid #bfcbd8;border-radius:5px;max-width:280px;width:100%;box-sizing:border-box}label{display:block;font-weight:600;margin:10px 0 4px}button{padding:9px 15px;border:0;background:#245ab0;color:white;border-radius:6px;cursor:pointer;margin:9px 0}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px}small{color:#66758a}a{color:#2759ad}.num{text-align:right;white-space:nowrap}</style></head><body><header>Монтажные ведомости · <a href="/app/revisions/{{.RevisionID}}/ledger">К редактору</a> · <a href="/app/revisions/{{.RevisionID}}/execution/export">Выгрузить XLSX</a></header><main><h1>Факт работ и начисления монтажникам</h1><p>{{.ProjectName}} · ревизия {{.Status}}</p><p><small>Начисления рассчитываются только по утверждённым событиям. Ставка монтажника независима от цены заказчика. Учётная единица факта совпадает с тарифицируемой единицей строки.</small></p>
<section><h2>План и общий факт</h2><table><tr><th>Работа</th><th>Объём по ведомости</th><th>Зафиксировано</th></tr>{{range .Items}}<tr><td>{{.Name}}</td><td>{{.Quantity}} {{.Unit}}</td><td>{{.Recorded}} {{.Unit}}</td></tr>{{else}}<tr><td colspan="3">Работ нет</td></tr>{{end}}</table></section>
{{if .CanWrite}}{{if eq .Status "draft"}}<section><h2>Назначить монтажника и фиксировать его ставку</h2><form method="post" action="/app/revisions/{{.RevisionID}}/execution/assign"><input type="hidden" name="csrf" value="{{.CSRF}}"><div class="grid"><div><label>Работа</label><select name="item" required>{{range .Items}}<option value="{{.ID}}">{{.Name}} ({{.Unit}})</option>{{end}}</select></div><div><label>Работник, закреплённый за объектом</label><select name="worker" required>{{range .Workers}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div><div><label>Ставка монтажника, ₽ за тарифицируемую единицу</label><input name="rate" inputmode="decimal" value="0" required></div></div><button>Создать назначение</button></form></section>{{end}}{{end}}
<section><h2>Назначения и выполненные объёмы</h2><table><tr><th>Монтажник / работа</th><th>Ставка</th><th>Записано</th><th>Утверждено</th><th>Начислено</th></tr>{{range .Assignments}}<tr><td>{{.Worker}} · {{.Name}}</td><td class="num">{{.Rate}} ₽/{{.Unit}}</td><td class="num">{{.Recorded}} {{.Unit}}</td><td class="num">{{.Approved}} {{.Unit}}</td><td class="num">{{.Accrued}} ₽</td></tr>{{else}}<tr><td colspan="5">Назначений нет</td></tr>{{end}}</table>
{{if .CanWrite}}<h2>Записать факт / корректировку</h2><p><small>Положительное количество — выполнено. Отрицательная корректировка вводится отдельным событием. Удаление и редактирование событий запрещены.</small></p>{{range .Assignments}}<form method="post" action="/app/revisions/{{$.RevisionID}}/execution/{{.ID}}/record"><input type="hidden" name="csrf" value="{{$.CSRF}}"><b>{{.Worker}} — {{.Name}}</b><div class="grid"><div><label>Действие</label><select name="event_type"><option value="performed">Выполнено</option><option value="correction">Корректировка (вычесть)</option></select></div><div><label>Объём (положительное число), {{.Unit}}</label><input name="quantity" value="1" required inputmode="decimal"></div><div><label>Дата выполнения</label><input name="performed_on" type="date" required></div><div><label>Комментарий</label><input name="note" maxlength="500"></div></div><button>Записать</button></form>{{end}}{{end}}</section>
<section><h2>Журнал событий</h2><table><tr><th>Дата</th><th>Монтажник</th><th>Работа</th><th>Изменение объёма</th><th>Статус</th><th>Комментарий</th><th></th></tr>{{range .Events}}<tr><td>{{.Date}}</td><td>{{.Worker}}</td><td>{{.Work}}</td><td>{{.Quantity}}</td><td>{{.Status}}</td><td>{{.Note}}</td><td>{{if .CanApprove}}<form method="post" action="/app/revisions/{{$.RevisionID}}/execution/{{.ID}}/approve"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Утвердить</button></form>{{end}}</td></tr>{{else}}<tr><td colspan="7">Событий нет</td></tr>{{end}}</table></section><section><h2>Итого начислено по утверждённым объёмам: {{.TotalApproved}} ₽</h2><p><small>Начислено — расчётная сумма, не подтверждение выплаты. Учет оплаты и налогов не реализован.</small></p></section></main></body></html>`

var executionTemplate = template.Must(template.New("execution").Parse(executionHTML))

func writeExecution(w http.ResponseWriter, d executionData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_ = executionTemplate.Execute(w, d)
}

func (x web) executionAccess(w http.ResponseWriter, r *http.Request, u auth.Identity, write bool, approve bool) (string, string, string, bool) {
	id := paramID(w, r)
	if id == "" {
		return "", "", "", false
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	project, status, name, err := x.revisionAccess(ctx, id, u)
	if err != nil {
		http.Error(w, "Ревизия не найдена", 404)
		return "", "", "", false
	}
	perm, err := x.projectPermission(ctx, project, u)
	if err != nil || (write && !canEdit(perm)) || (approve && perm != "admin" && perm != "approve") {
		http.Error(w, "Доступ запрещён", 403)
		return "", "", "", false
	}
	return project, status, name, true
}
func (x web) executionGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	project, status, name, ok := x.executionAccess(w, r, u, false, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	perm, err := x.projectPermission(ctx, project, u)
	if err != nil || perm == "read" {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	d := executionData{RevisionID: r.PathValue("id"), ProjectName: name, Status: status, CSRF: x.csrf(r, u), CanWrite: canEdit(perm), CanApprove: perm == "admin" || perm == "approve", TotalApproved: "0.00"}
	rows, err := x.db.Query(ctx, `SELECT i.id::text,i.name_snapshot,i.billable_unit_code,i.billable_quantity::text,COALESCE(sum(e.quantity_delta),0)::text FROM project_work_items i LEFT JOIN work_assignments a ON a.work_item_id=i.id LEFT JOIN work_execution_events e ON e.assignment_id=a.id WHERE i.revision_id=$1 GROUP BY i.id ORDER BY i.sort_order,i.id`, d.RevisionID)
	if err != nil {
		http.Error(w, "Ошибка загрузки работ", 500)
		return
	}
	for rows.Next() {
		var v executionItem
		if err = rows.Scan(&v.ID, &v.Name, &v.Unit, &v.Quantity, &v.Recorded); err != nil {
			break
		}
		d.Items = append(d.Items, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка чтения работ", 500)
		return
	}
	rows, err = x.db.Query(ctx, `SELECT w.id::text,w.display_name FROM project_workers pw JOIN workers w ON w.id=pw.worker_id WHERE pw.project_id=$1 AND w.active ORDER BY w.display_name`, project)
	if err != nil {
		http.Error(w, "Ошибка списка работников", 500)
		return
	}
	for rows.Next() {
		var v executionWorker
		if err = rows.Scan(&v.ID, &v.Name); err != nil {
			break
		}
		d.Workers = append(d.Workers, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка чтения работников", 500)
		return
	}
	rows, err = x.db.Query(ctx, `SELECT a.id::text,i.name_snapshot,w.display_name,r.unit_code,r.rate_per_billable_unit::text,COALESCE(sum(e.quantity_delta),0)::text,COALESCE(sum(e.quantity_delta) FILTER(WHERE ap.event_id IS NOT NULL),0)::text,ROUND(COALESCE(sum(e.quantity_delta) FILTER(WHERE ap.event_id IS NOT NULL),0)*r.rate_per_billable_unit,2)::text FROM work_assignments a JOIN project_work_items i ON i.id=a.work_item_id JOIN workers w ON w.id=a.worker_id JOIN worker_rate_snapshots r ON r.assignment_id=a.id LEFT JOIN work_execution_events e ON e.assignment_id=a.id LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id WHERE a.revision_id=$1 GROUP BY a.id,i.name_snapshot,w.display_name,r.unit_code,r.rate_per_billable_unit ORDER BY w.display_name,i.name_snapshot`, d.RevisionID)
	if err != nil {
		http.Error(w, "Ошибка назначений", 500)
		return
	}
	for rows.Next() {
		var v executionAssignment
		if err = rows.Scan(&v.ID, &v.Name, &v.Worker, &v.Unit, &v.Rate, &v.Recorded, &v.Approved, &v.Accrued); err != nil {
			break
		}
		d.Assignments = append(d.Assignments, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка чтения назначений", 500)
		return
	}
	rows, err = x.db.Query(ctx, `SELECT e.id::text,w.display_name,i.name_snapshot,e.quantity_delta::text,e.performed_on::text,COALESCE(e.notes,''),CASE WHEN ap.event_id IS NULL THEN 'Ожидает утверждения' ELSE 'Утверждено' END,ap.event_id IS NULL FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id JOIN project_work_items i ON i.id=a.work_item_id JOIN workers w ON w.id=a.worker_id LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id WHERE a.revision_id=$1 ORDER BY e.created_at DESC,e.id DESC LIMIT 500`, d.RevisionID)
	if err != nil {
		http.Error(w, "Ошибка журнала", 500)
		return
	}
	for rows.Next() {
		var v executionEvent
		var pending bool
		if err = rows.Scan(&v.ID, &v.Worker, &v.Work, &v.Quantity, &v.Date, &v.Note, &v.Status, &pending); err != nil {
			break
		}
		v.CanApprove = pending && d.CanApprove
		d.Events = append(d.Events, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка чтения журнала", 500)
		return
	}
	// Accurate calculation from numeric columns, not float conversion.
	err = x.db.QueryRow(ctx, `SELECT COALESCE(sum(amount),0)::text FROM (SELECT ROUND(COALESCE(sum(e.quantity_delta),0)*r.rate_per_billable_unit,2) amount FROM work_assignments a JOIN worker_rate_snapshots r ON r.assignment_id=a.id LEFT JOIN work_execution_events e ON e.assignment_id=a.id AND EXISTS(SELECT 1 FROM work_execution_approvals ap WHERE ap.event_id=e.id) WHERE a.revision_id=$1 GROUP BY a.id,r.rate_per_billable_unit) q`, d.RevisionID).Scan(&d.TotalApproved)
	if err != nil {
		http.Error(w, "Ошибка расчёта начислений", 500)
		return
	}
	writeExecution(w, d)
}

func (x web) executionAssign(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	project, status, _, ok := x.executionAccess(w, r, u, true, false)
	if !ok {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	if status != "draft" {
		http.Error(w, "Назначение возможно только в черновике", 409)
		return
	}
	item, worker := r.PostFormValue("item"), r.PostFormValue("worker")
	rate, err := calculation.Parse(r.PostFormValue("rate"), 4)
	if !uuidPattern.MatchString(item) || !uuidPattern.MatchString(worker) || err != nil || rate.Sign() < 0 {
		http.Error(w, "Некорректные данные", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Ошибка БД", 500)
		return
	}
	defer tx.Rollback(ctx)
	// Locks revision so approval/change cannot race with the assignment.
	var revstatus string
	err = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&revstatus)
	if err != nil || revstatus != "draft" {
		http.Error(w, "Ревизия больше не является черновиком", 409)
		return
	}
	var assignmentID string
	err = tx.QueryRow(ctx, `INSERT INTO work_assignments(revision_id,work_item_id,worker_id,planned_quantity) SELECT i.revision_id,i.id,pw.worker_id,0 FROM project_work_items i JOIN project_revisions rev ON rev.id=i.revision_id JOIN project_workers pw ON pw.project_id=rev.project_id AND pw.worker_id=$3 WHERE i.id=$2 AND i.revision_id=$1 RETURNING id`, r.PathValue("id"), item, worker).Scan(&assignmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Работа или монтажник не найден", 404)
		return
	}
	if err != nil {
		http.Error(w, "Назначение уже существует или недоступно", 409)
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO worker_rate_snapshots(assignment_id,rate_per_billable_unit,unit_code) SELECT $1,$2,i.billable_unit_code FROM project_work_items i WHERE i.id=$3 AND i.revision_id=$4`, assignmentID, calculation.Fixed(rate, 4), item, r.PathValue("id"))
	if err != nil {
		http.Error(w, "Не удалось сохранить ставку", 409)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		http.Error(w, "Ошибка сохранения", 500)
		return
	}
	_ = project
	http.Redirect(w, r, "/app/revisions/"+r.PathValue("id")+"/execution", http.StatusSeeOther)
}

func (x web) executionRecord(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	_, _, _, ok := x.executionAccess(w, r, u, true, false)
	if !ok {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	assignment := r.PathValue("assignment")
	if !uuidPattern.MatchString(assignment) {
		http.Error(w, "Некорректное назначение", 400)
		return
	}
	typ := r.PostFormValue("event_type")
	if typ != "performed" && typ != "correction" {
		http.Error(w, "Некорректное действие", 400)
		return
	}
	qty, err := calculation.Parse(r.PostFormValue("quantity"), 8)
	if err != nil || qty.Sign() <= 0 {
		http.Error(w, "Количество должно быть положительным", 400)
		return
	}
	when, err := time.Parse("2006-01-02", r.PostFormValue("performed_on"))
	if err != nil || when.After(time.Now().Add(24*time.Hour)) {
		http.Error(w, "Неверная дата", 400)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("note"))
	if len(note) > 500 {
		http.Error(w, "Комментарий слишком длинный", 400)
		return
	}
	delta := calculation.Fixed(qty, 8)
	if typ == "correction" {
		delta = "-" + delta
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tag, err := x.db.Exec(ctx, `INSERT INTO work_execution_events(assignment_id,event_type,quantity_delta,performed_on,created_by,notes) SELECT a.id,$3,$4,$5,$6,$7 FROM work_assignments a WHERE a.id=$1 AND a.revision_id=$2`, assignment, r.PathValue("id"), typ, delta, when.Format("2006-01-02"), u.ID, note)
	if err != nil {
		fmt.Printf("ERROR executionRecord: %v\n", err)

		http.Error(
			w,
			"Не удалось записать выполненный объём. Подробности в журнале сервера.",
			http.StatusConflict,
		)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "Назначение не найдено", 404)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+r.PathValue("id")+"/execution", http.StatusSeeOther)
}

func (x web) executionApprove(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	_, _, _, ok := x.executionAccess(w, r, u, false, true)
	if !ok {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	event := r.PathValue("event")
	if !uuidPattern.MatchString(event) {
		http.Error(w, "Некорректное событие", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tag, err := x.db.Exec(ctx, `INSERT INTO work_execution_approvals(event_id,approved_by) SELECT e.id,$3 FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id WHERE e.id=$1 AND a.revision_id=$2 ON CONFLICT(event_id) DO NOTHING`, event, r.PathValue("id"), u.ID)
	if err != nil {
		http.Error(w, "Ошибка утверждения", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "Событие не найдено или уже утверждено", 409)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+r.PathValue("id")+"/execution", http.StatusSeeOther)
}

func (x web) executionExport(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	project, _, _, ok := x.executionAccess(w, r, u, false, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	perm, err := x.projectPermission(ctx, project, u)
	if err != nil || perm == "read" {
		http.Error(w, "Доступ запрещён", http.StatusForbidden)
		return
	}
	rows, err := x.db.Query(ctx, `SELECT w.display_name,i.name_snapshot,r.unit_code,COALESCE(sum(e.quantity_delta),0)::text,r.rate_per_billable_unit::text,ROUND(COALESCE(sum(e.quantity_delta),0)*r.rate_per_billable_unit,2)::text FROM work_assignments a JOIN workers w ON w.id=a.worker_id JOIN project_work_items i ON i.id=a.work_item_id JOIN worker_rate_snapshots r ON r.assignment_id=a.id LEFT JOIN work_execution_events e ON e.assignment_id=a.id AND EXISTS(SELECT 1 FROM work_execution_approvals ap WHERE ap.event_id=e.id) WHERE a.revision_id=$1 GROUP BY a.id,w.display_name,i.name_snapshot,r.unit_code,r.rate_per_billable_unit ORDER BY w.display_name,i.name_snapshot`, r.PathValue("id"))
	if err != nil {
		http.Error(w, "Ошибка выгрузки", 500)
		return
	}
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Начисления"
	_ = f.SetSheetName("Sheet1", sheet)
	headings := []string{"Работник", "Монтажная работа", "Ед.", "Утверждено", "Ставка, ₽", "Начислено, ₽"}
	for c, v := range headings {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		_ = f.SetCellStr(sheet, cell, v)
	}
	n := 2
	for rows.Next() {
		var worker, item, unit, qty, rate, amount string
		if err = rows.Scan(&worker, &item, &unit, &qty, &rate, &amount); err != nil {
			break
		}
		vals := []string{worker, item, unit, qty, rate, amount}
		for c, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(c+1, n)
			_ = f.SetCellStr(sheet, cell, safeExcelText(v))
		}
		n++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка выгрузки", 500)
		return
	}
	_ = f.SetColWidth(sheet, "A", "A", 25)
	_ = f.SetColWidth(sheet, "B", "B", 46)
	_ = f.SetColWidth(sheet, "C", "F", 18)
	var total string
	err = x.db.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0)::text FROM (SELECT ROUND(COALESCE(SUM(e.quantity_delta),0)*r.rate_per_billable_unit,2) amount FROM work_assignments a JOIN worker_rate_snapshots r ON r.assignment_id=a.id LEFT JOIN work_execution_events e ON e.assignment_id=a.id AND EXISTS(SELECT 1 FROM work_execution_approvals ap WHERE ap.event_id=e.id) WHERE a.revision_id=$1 GROUP BY a.id,r.rate_per_billable_unit) q`, r.PathValue("id")).Scan(&total)
	if err != nil {
		http.Error(w, "Ошибка начислений", 500)
		return
	}
	_ = f.SetCellStr(sheet, fmt.Sprintf("E%d", n), "Итого")
	_ = f.SetCellStr(sheet, fmt.Sprintf("F%d", n), total)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="worker-accruals.xlsx"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if err = f.Write(w); err != nil {
		return
	}
}

// Escape dangerous spreadsheet prefixes; writing as strings alone is not a security boundary.
func safeExcelText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

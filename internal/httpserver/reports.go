package httpserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xuri/excelize/v2"
	"montage-ledger/internal/auth"
	"montage-ledger/internal/calculation"
)

type reportWorker struct {
	ID, Name, PeriodAccrued, TotalAccrued, PeriodPaid, TotalPaid, Balance string
}
type reportLine struct{ System, Work, Unit, Planned, Recorded, Pending, Approved, Remaining string }
type reportPayment struct{ Date, Worker, Amount, Note string }
type reportData struct {
	RevisionID, ProjectName, PeriodStart, PeriodEnd, WorkerFilter, CSRF string
	CanPay                                                              bool
	Workers                                                             []reportWorker
	Lines                                                               []reportLine
	Payments                                                            []reportPayment
	PeriodAccrued, PeriodPaid, TotalAccrued, TotalPaid, Balance         string
}

const reportsHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Сводный отчёт — монтажные ведомости</title><style>body{font:15px/1.5 system-ui,sans-serif;background:#f5f7fa;color:#182236;margin:0}header{background:#182743;color:#fff;padding:17px 5%}header a{color:#fff}main{max-width:1300px;margin:24px auto;padding:0 18px}section{background:#fff;border:1px solid #dae2ec;border-radius:10px;padding:20px;margin:18px 0;overflow:auto}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #e4e8ef;padding:9px;text-align:left}input,select{padding:9px;border:1px solid #bfcbd8;border-radius:5px;box-sizing:border-box;width:100%}label{display:block;font-weight:600;margin:10px 0 4px}button{padding:9px 15px;border:0;background:#245ab0;color:#fff;border-radius:6px;cursor:pointer;margin:9px 0}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px}.stat{padding:15px;border:1px solid #dee6ef;border-radius:8px}.stat b{font-size:21px}small{color:#66758a}.num{text-align:right;white-space:nowrap}a{color:#2759ad}</style><link rel="stylesheet" href="/assets/app.css"></head><body><header><a href="/app/revisions/{{.RevisionID}}/execution">Факт и начисления</a> · <a href="/app/revisions/{{.RevisionID}}/ledger">Ведомость</a></header><main><h1>Сводный отчёт по ревизии</h1><p>{{.ProjectName}}</p><section><form method="get"><div class="grid"><div><label>Период с</label><input type="date" name="from" value="{{.PeriodStart}}" required></div><div><label>Период по</label><input type="date" name="to" value="{{.PeriodEnd}}" required></div><div><label>Монтажник</label><select name="worker"><option value="">Все монтажники</option>{{range .Workers}}<option value="{{.ID}}" {{if eq .ID $.WorkerFilter}}selected{{end}}>{{.Name}}</option>{{end}}</select></div></div><button>Применить фильтры</button></form><p><a href="/app/revisions/{{.RevisionID}}/reports/export?from={{.PeriodStart}}&amp;to={{.PeriodEnd}}&amp;worker={{.WorkerFilter}}">Экспорт XLSX с выбранными фильтрами</a></p><small>Начисления за период определяются по дате выполнения, выплаты — по дате платежа. В расчёт начислений попадают только утверждённые события, независимо от даты утверждения. Остаток к выплате показан за всё время ревизии.</small></section>
<section><h2>Финансовая сводка</h2><div class="grid"><div class="stat"><small>Начислено за период</small><p><b>{{.PeriodAccrued}} ₽</b></p></div><div class="stat"><small>Выплачено за период</small><p><b>{{.PeriodPaid}} ₽</b></p></div><div class="stat"><small>Начислено за всё время</small><p><b>{{.TotalAccrued}} ₽</b></p></div><div class="stat"><small>Выплачено за всё время</small><p><b>{{.TotalPaid}} ₽</b></p></div><div class="stat"><small>Остаток к выплате</small><p><b>{{.Balance}} ₽</b></p></div></div></section>
<section><h2>Начисления и выплаты по монтажникам</h2><table><tr><th>Работник</th><th>Начислено за период</th><th>Выплачено за период</th><th>Начислено всего</th><th>Выплачено всего</th><th>Остаток</th></tr>{{range .Workers}}<tr><td>{{.Name}}</td><td class="num">{{.PeriodAccrued}}</td><td class="num">{{.PeriodPaid}}</td><td class="num">{{.TotalAccrued}}</td><td class="num">{{.TotalPaid}}</td><td class="num">{{.Balance}}</td></tr>{{else}}<tr><td colspan="6">Работники не найдены</td></tr>{{end}}</table></section>
<section><h2>Выполнение по монтажным работам — за всё время</h2><small>Объёмы разных единиц не суммируются. Записано включает ожидающие утверждения события; показатели сгруппированы по строке, а не по количеству монтажников.</small><table><tr><th>Система</th><th>Работа</th><th>План</th><th>Записано</th><th>Ожидает</th><th>Утверждено</th><th>Осталось</th></tr>{{range .Lines}}<tr><td>{{.System}}</td><td>{{.Work}}</td><td class="num">{{.Planned}} {{.Unit}}</td><td class="num">{{.Recorded}}</td><td class="num">{{.Pending}}</td><td class="num">{{.Approved}}</td><td class="num">{{.Remaining}}</td></tr>{{else}}<tr><td colspan="7">Нет работ</td></tr>{{end}}</table></section>
{{if .CanPay}}<section><h2>Зарегистрировать выплату</h2><p><small>Выплата записывается в неизменяемый журнал. Система запрещает выплатить больше утверждённого заработка. Только администратор может регистрировать выплаты.</small></p><form method="post" action="/app/revisions/{{.RevisionID}}/reports/payments"><input type="hidden" name="csrf" value="{{.CSRF}}"><div class="grid"><div><label>Монтажник</label><select name="worker" required>{{range .Workers}}<option value="{{.ID}}">{{.Name}} (остаток {{.Balance}} ₽)</option>{{end}}</select></div><div><label>Сумма, ₽</label><input name="amount" inputmode="decimal" required></div><div><label>Дата выплаты</label><input name="paid_on" type="date" required></div><div><label>Примечание</label><input name="note" maxlength="500"></div></div><button>Зарегистрировать выплату</button></form></section>{{end}}
<section><h2>Журнал выплат за выбранный период</h2><table><tr><th>Дата</th><th>Монтажник</th><th>Сумма</th><th>Примечание</th></tr>{{range .Payments}}<tr><td>{{.Date}}</td><td>{{.Worker}}</td><td class="num">{{.Amount}} ₽</td><td>{{.Note}}</td></tr>{{else}}<tr><td colspan="4">Выплат нет</td></tr>{{end}}</table></section></main></body></html>`

var reportsTemplate = template.Must(template.New("reports").Parse(reportsHTML))

func reportPeriod(r *http.Request, now time.Time) (string, string, string, error) {
	start := r.URL.Query().Get("from")
	end := r.URL.Query().Get("to")
	if start == "" {
		start = now.Format("2006-01") + "-01"
	}
	if end == "" {
		end = now.Format("2006-01-02")
	}
	a, e := time.Parse("2006-01-02", start)
	if e != nil || a.Format("2006-01-02") != start {
		return "", "", "", fmt.Errorf("invalid start")
	}
	b, e := time.Parse("2006-01-02", end)
	if e != nil || b.Format("2006-01-02") != end || b.Before(a) {
		return "", "", "", fmt.Errorf("invalid end")
	}
	worker := r.URL.Query().Get("worker")
	if worker != "" && !uuidPattern.MatchString(worker) {
		return "", "", "", fmt.Errorf("invalid worker")
	}
	return start, end, worker, nil
}

func (x web) reportLoad(ctx context.Context, revision, project, name, start, end, worker string) (reportData, error) {
	d := reportData{RevisionID: revision, ProjectName: name, PeriodStart: start, PeriodEnd: end, WorkerFilter: worker, PeriodAccrued: "0", PeriodPaid: "0", TotalAccrued: "0", TotalPaid: "0", Balance: "0"}
	// Aggregates each assignment independently, then sums rounded assignment accruals.
	// A lateral aggregate prevents multiplying amounts by the number of payments/events.
	rows, err := x.db.Query(ctx, `SELECT w.id::text,w.display_name,
 COALESCE(a.period_amount,0)::text,COALESCE(a.all_amount,0)::text,
 COALESCE(p.period_amount,0)::text,COALESCE(p.all_amount,0)::text,
 (COALESCE(a.all_amount,0)-COALESCE(p.all_amount,0))::text
 FROM project_workers pw JOIN workers w ON w.id=pw.worker_id
 LEFT JOIN LATERAL (
  SELECT COALESCE(SUM(ROUND(COALESCE(q.period_qty,0)*r.rate_per_billable_unit,2)),0) AS period_amount,
         COALESCE(SUM(ROUND(COALESCE(q.all_qty,0)*r.rate_per_billable_unit,2)),0) AS all_amount
  FROM work_assignments wa JOIN worker_rate_snapshots r ON r.assignment_id=wa.id
  LEFT JOIN LATERAL (
    SELECT SUM(e.quantity_delta) all_qty,
           SUM(e.quantity_delta) FILTER (WHERE e.performed_on BETWEEN $3::date AND $4::date) period_qty
    FROM work_execution_events e JOIN work_execution_approvals ap ON ap.event_id=e.id
    WHERE e.assignment_id=wa.id
  ) q ON true
  WHERE wa.revision_id=$1 AND wa.worker_id=w.id
 ) a ON true
 LEFT JOIN LATERAL (
  SELECT COALESCE(SUM(py.amount),0) all_amount,
         COALESCE(SUM(py.amount) FILTER (WHERE py.paid_on BETWEEN $3::date AND $4::date),0) period_amount
  FROM project_worker_payments py WHERE py.revision_id=$1 AND py.worker_id=w.id
 ) p ON true
 WHERE pw.project_id=$2 AND ($5::text='' OR w.id::text=$5::text)
 ORDER BY w.display_name,w.id`, revision, project, start, end, worker)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var v reportWorker
		if err = rows.Scan(&v.ID, &v.Name, &v.PeriodAccrued, &v.TotalAccrued, &v.PeriodPaid, &v.TotalPaid, &v.Balance); err != nil {
			break
		}
		d.Workers = append(d.Workers, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return d, err
	}
	// Totals from the same per-worker rounded amounts, so the visible table reconciles.
	var totalPeriodAcc, totalPeriodPaid, totalAcc, totalPaid string
	err = x.db.QueryRow(ctx, `SELECT COALESCE(SUM(period_accrued),0)::text,COALESCE(SUM(period_paid),0)::text,
 COALESCE(SUM(total_accrued),0)::text,COALESCE(SUM(total_paid),0)::text FROM (
 SELECT w.id,
 COALESCE(a.period_amount,0) period_accrued,COALESCE(a.all_amount,0) total_accrued,
 COALESCE(p.period_amount,0) period_paid,COALESCE(p.all_amount,0) total_paid
 FROM project_workers pw JOIN workers w ON w.id=pw.worker_id
 LEFT JOIN LATERAL (
 SELECT COALESCE(SUM(ROUND(COALESCE(q.period_qty,0)*r.rate_per_billable_unit,2)),0) period_amount,
 COALESCE(SUM(ROUND(COALESCE(q.all_qty,0)*r.rate_per_billable_unit,2)),0) all_amount
 FROM work_assignments wa JOIN worker_rate_snapshots r ON r.assignment_id=wa.id
 LEFT JOIN LATERAL (
 SELECT SUM(e.quantity_delta) all_qty,SUM(e.quantity_delta) FILTER (WHERE e.performed_on BETWEEN $3::date AND $4::date) period_qty
 FROM work_execution_events e JOIN work_execution_approvals ap ON ap.event_id=e.id WHERE e.assignment_id=wa.id
 ) q ON true WHERE wa.revision_id=$1 AND wa.worker_id=w.id
 ) a ON true
 LEFT JOIN LATERAL (
 SELECT COALESCE(SUM(py.amount),0) all_amount,COALESCE(SUM(py.amount) FILTER (WHERE py.paid_on BETWEEN $3::date AND $4::date),0) period_amount
 FROM project_worker_payments py WHERE py.revision_id=$1 AND py.worker_id=w.id
 ) p ON true
 WHERE pw.project_id=$2 AND ($5::text='' OR w.id::text=$5::text)
 ) totals`, revision, project, start, end, worker).Scan(&totalPeriodAcc, &totalPeriodPaid, &totalAcc, &totalPaid)
	if err != nil {
		return d, err
	}
	d.PeriodAccrued = totalPeriodAcc
	d.PeriodPaid = totalPeriodPaid
	d.TotalAccrued = totalAcc
	d.TotalPaid = totalPaid
	d.Balance = subtractDecimal(totalAcc, totalPaid)
	rows, err = x.db.Query(ctx, `SELECT s.code,i.name_snapshot,i.billable_unit_code,i.billable_quantity::text,
 COALESCE(t.recorded,0)::text,COALESCE(t.pending,0)::text,COALESCE(t.approved,0)::text,
 (i.billable_quantity-COALESCE(t.recorded,0))::text
 FROM project_work_items i JOIN project_systems s ON s.id=i.system_id AND s.revision_id=i.revision_id
 LEFT JOIN LATERAL (
 SELECT SUM(e.quantity_delta) recorded,
 SUM(e.quantity_delta) FILTER (WHERE ap.event_id IS NULL) pending,
 SUM(e.quantity_delta) FILTER (WHERE ap.event_id IS NOT NULL) approved
 FROM work_assignments wa JOIN work_execution_events e ON e.assignment_id=wa.id
 LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id
 WHERE wa.work_item_id=i.id AND ($2::text='' OR wa.worker_id::text=$2::text)
 ) t ON true
 WHERE i.revision_id=$1 ORDER BY s.sort_order,i.sort_order,i.id`, revision, worker)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var v reportLine
		if err = rows.Scan(&v.System, &v.Work, &v.Unit, &v.Planned, &v.Recorded, &v.Pending, &v.Approved, &v.Remaining); err != nil {
			break
		}
		d.Lines = append(d.Lines, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = x.db.Query(ctx, `SELECT py.paid_on::text,w.display_name,py.amount::text,py.notes
 FROM project_worker_payments py JOIN workers w ON w.id=py.worker_id
 WHERE py.revision_id=$1 AND py.paid_on BETWEEN $2::date AND $3::date AND ($4::text='' OR py.worker_id::text=$4::text)
 ORDER BY py.paid_on DESC,py.created_at DESC,py.id DESC`, revision, start, end, worker)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var v reportPayment
		if err = rows.Scan(&v.Date, &v.Worker, &v.Amount, &v.Note); err != nil {
			break
		}
		d.Payments = append(d.Payments, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return d, err
}

func (x web) reportAccess(w http.ResponseWriter, r *http.Request, u auth.Identity) (reportData, bool) {
	revision := paramID(w, r)
	if revision == "" {
		return reportData{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	project, _, name, err := x.revisionAccess(ctx, revision, u)
	if err != nil {
		http.Error(w, "Ревизия не найдена", 404)
		return reportData{}, false
	}
	perm, err := x.projectPermission(ctx, project, u)
	if err != nil {
		http.Error(w, "Доступ запрещён", 403)
		return reportData{}, false
	}
	start, end, worker, err := reportPeriod(r, time.Now())
	if err != nil {
		http.Error(w, "Неверный период или идентификатор работника", 400)
		return reportData{}, false
	}
	d, err := x.reportLoad(ctx, revision, project, name, start, end, worker)
	if err != nil {
		slog.Error("report load failed", "revision", revision, "error", err)
		http.Error(w, "Ошибка подготовки отчёта", 500)
		return reportData{}, false
	}
	d.CanPay = perm == "admin"
	d.CSRF = x.csrf(r, u)
	return d, true
}
func (x web) reportGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	d, ok := x.reportAccess(w, r, u)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_ = reportsTemplate.Execute(w, d)
}
func (x web) reportExport(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	d, ok := x.reportAccess(w, r, u)
	if !ok {
		return
	}
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "Работники")
	head := []string{"Работник", "Начислено за период, ₽", "Выплачено за период, ₽", "Начислено всего, ₽", "Выплачено всего, ₽", "Остаток к выплате, ₽"}
	for i, v := range head {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellStr("Работники", cell, v)
	}
	for idx, v := range d.Workers {
		values := []string{v.Name, v.PeriodAccrued, v.PeriodPaid, v.TotalAccrued, v.TotalPaid, v.Balance}
		for col, s := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, idx+2)
			_ = f.SetCellStr("Работники", cell, safeExcelText(s))
		}
	}
	_ = f.SetColWidth("Работники", "A", "A", 30)
	_ = f.SetColWidth("Работники", "B", "F", 22)
	_, _ = f.NewSheet("Работы")
	for i, v := range []string{"Система", "Работа", "Ед.", "План", "Записано", "Ожидает", "Утверждено", "Осталось"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellStr("Работы", cell, v)
	}
	for i, v := range d.Lines {
		values := []string{v.System, v.Work, v.Unit, v.Planned, v.Recorded, v.Pending, v.Approved, v.Remaining}
		for j, s := range values {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			_ = f.SetCellStr("Работы", cell, safeExcelText(s))
		}
	}
	_ = f.SetColWidth("Работы", "B", "B", 50)
	_, _ = f.NewSheet("Выплаты")
	for i, v := range []string{"Дата", "Работник", "Сумма, ₽", "Комментарий"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellStr("Выплаты", cell, v)
	}
	for i, v := range d.Payments {
		values := []string{v.Date, v.Worker, v.Amount, v.Note}
		for j, s := range values {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			_ = f.SetCellStr("Выплаты", cell, safeExcelText(s))
		}
	}
	_ = f.SetColWidth("Выплаты", "B", "B", 30)
	_ = f.SetColWidth("Выплаты", "D", "D", 42)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="revision-report.xlsx"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_ = f.Write(w)
}
func (x web) paymentPost(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	revision := paramID(w, r)
	if revision == "" {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	project, _, _, err := x.revisionAccess(ctx, revision, u)
	if err != nil {
		http.Error(w, "Ревизия не найдена", 404)
		return
	}
	perm, err := x.projectPermission(ctx, project, u)
	if err != nil || perm != "admin" {
		http.Error(w, "Выплаты может регистрировать только администратор объекта", 403)
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	worker := r.PostFormValue("worker")
	if !uuidPattern.MatchString(worker) {
		http.Error(w, "Некорректный работник", 400)
		return
	}
	amount, err := calculation.Parse(r.PostFormValue("amount"), 2)
	if err != nil || amount.Sign() <= 0 {
		http.Error(w, "Сумма должна быть положительной, точность до копейки", 400)
		return
	}
	date := r.PostFormValue("paid_on")
	t, err := time.Parse("2006-01-02", date)
	if err != nil || t.Format("2006-01-02") != date || t.After(time.Now().Add(24*time.Hour)) {
		http.Error(w, "Неверная дата выплаты", 400)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("note"))
	if len(note) > 500 {
		http.Error(w, "Слишком длинное примечание", 400)
		return
	}
	tag, err := x.db.Exec(ctx, `INSERT INTO project_worker_payments(revision_id,worker_id,amount,paid_on,notes,created_by)
 SELECT $1,w.id,$3,$4,$5,$6 FROM workers w JOIN project_workers pw ON pw.worker_id=w.id WHERE w.id=$2 AND pw.project_id=$7`, revision, worker, calculation.Fixed(amount, 2), date, note, u.ID, project)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && (pgerr.Code == "P0001" || pgerr.Code == "23514") {
			http.Error(w, "Выплата превышает утверждённый остаток либо нарушает ограничения", 409)
			return
		}
		http.Error(w, "Не удалось сохранить выплату", 500)
		return
	}
	if tag.RowsAffected() != 1 {
		http.Error(w, "Работник не закреплён за объектом", 404)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+revision+"/reports", http.StatusSeeOther)
}

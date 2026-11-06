package httpserver

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"montage-ledger/internal/auth"
)

// Progress is averaged across individual work items, never across incompatible units.
// Recorded progress includes pending events; approved progress includes approved events only.
type progressSystem struct {
	Code, Name, Items, Complete, RecordedPercent, ApprovedPercent, PendingEvents string
}
type progressWorker struct {
	Name, Assignments, PendingEvents, ApprovedEvents, Accrued string
}
type progressPending struct {
	ID, Worker, System, Work, Quantity, Unit, Date, Note string
}
type progressPage struct {
	RevisionID, ProjectName, Status, CSRF, TotalPending string
	CanApprove                                          bool
	Systems                                             []progressSystem
	Workers                                             []progressWorker
	Pending                                             []progressPending
}

const progressHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Прогресс монтажа</title><link rel="stylesheet" href="/assets/app.css"><style>body{font:15px/1.5 system-ui,sans-serif;background:#f5f7fa;color:#182236;margin:0}header{background:#182743;color:white;padding:18px 5%}header a{color:white}main{max-width:1280px;margin:25px auto;padding:0 18px}section{background:white;border:1px solid #dae2ec;border-radius:10px;padding:20px;margin:18px 0;overflow:auto}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #e4e8ef;padding:9px;text-align:left}td.num,th.num{white-space:nowrap;text-align:right}.meter{height:11px;background:#edf2f7;border-radius:9px;overflow:hidden;min-width:120px}.meter span{display:block;height:100%;background:#2369bd}small{color:#607086}button{background:#245ab0;color:white;border:none;border-radius:6px;padding:9px 14px;cursor:pointer}a{color:#245ab0}.muted{color:#607086}</style></head><body><header><a href="/app/revisions/{{.RevisionID}}/execution">Факт и начисления</a> · <a href="/app/revisions/{{.RevisionID}}/execution/schedule">Календарь работ</a> · <a href="/app/revisions/{{.RevisionID}}/reports">Сводный отчёт</a> · <a href="/app/revisions/{{.RevisionID}}/ledger">Ведомость</a></header><main><h1>Оперативный прогресс монтажа</h1><p>{{.ProjectName}} · {{.Status}}</p><p class="muted">Прогресс каждой системы — среднее выполнение её строк в процентах. Единицы измерения разных работ не суммируются. «Записано» включает ожидающие утверждения события, «Утверждено» — только подтверждённые.</p>
<section><h2>Выполнение по системам</h2><table><tr><th>Система</th><th class="num">Строк</th><th class="num">Закрыто по факту</th><th>Записано</th><th>Утверждено</th><th class="num">Ожидают решения</th></tr>{{range .Systems}}<tr><td><b>{{.Code}}</b> {{.Name}}</td><td class="num">{{.Items}}</td><td class="num">{{.Complete}}</td><td><progress max="100" value="{{.RecordedPercent}}">{{.RecordedPercent}}%</progress> {{.RecordedPercent}}%</td><td><progress max="100" value="{{.ApprovedPercent}}">{{.ApprovedPercent}}%</progress> {{.ApprovedPercent}}%</td><td class="num">{{.PendingEvents}}</td></tr>{{else}}<tr><td colspan="6">Системы отсутствуют</td></tr>{{end}}</table><p><small>Строка считается закрытой по факту, когда записанный объём достиг планового. Строки с нулевым планом исключены из среднего процента и не считаются завершёнными.</small></p></section>
<section><h2>Загрузка монтажников и начисления</h2><table><tr><th>Монтажник</th><th class="num">Назначений</th><th class="num">Ожидающих событий</th><th class="num">Утверждённых событий</th><th class="num">Начислено</th></tr>{{range .Workers}}<tr><td>{{.Name}}</td><td class="num">{{.Assignments}}</td><td class="num">{{.PendingEvents}}</td><td class="num">{{.ApprovedEvents}}</td><td class="num">{{.Accrued}} ₽</td></tr>{{else}}<tr><td colspan="5">Назначений нет</td></tr>{{end}}</table><p><small>Начисления — не выплаты. Сумма рассчитана по утверждённому объёму каждого назначения и зафиксированной ставке.</small></p></section>
<section><h2>Очередь утверждения · {{.TotalPending}} событий</h2><table><tr><th>Дата</th><th>Система / работа</th><th>Монтажник</th><th class="num">Объём</th><th>Комментарий</th><th></th></tr>{{range .Pending}}<tr><td>{{.Date}}</td><td>{{.System}} · {{.Work}}</td><td>{{.Worker}}</td><td class="num">{{.Quantity}} {{.Unit}}</td><td>{{.Note}}</td><td>{{if $.CanApprove}}<form method="post" action="/app/revisions/{{$.RevisionID}}/execution/{{.ID}}/approve"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Утвердить</button></form>{{else}}<small>Требуется право approve</small>{{end}}</td></tr>{{else}}<tr><td colspan="6">Все события рассмотрены</td></tr>{{end}}</table><p><small>Показаны первые 200 ожидающих событий в хронологическом порядке. Утверждение одного события выполняется существующим защищённым обработчиком.</small></p></section></main></body></html>`

var progressTemplate = template.Must(template.New("execution-progress").Parse(progressHTML))

// Exact unit-aware SQL: each item has its own numerator/denominator before system aggregation.
const progressSystemsSQL = `SELECT s.code, s.name,
 COUNT(i.id)::text,
 COUNT(i.id) FILTER (WHERE i.billable_quantity>0 AND COALESCE(q.recorded,0)>=i.billable_quantity)::text,
 COALESCE(ROUND(AVG(CASE WHEN i.billable_quantity>0 THEN LEAST(100::numeric,100*COALESCE(q.recorded,0)/i.billable_quantity) END),1),0)::text,
 COALESCE(ROUND(AVG(CASE WHEN i.billable_quantity>0 THEN LEAST(100::numeric,100*COALESCE(q.approved,0)/i.billable_quantity) END),1),0)::text,
 COALESCE(SUM(q.pending_events),0)::text
 FROM project_systems s LEFT JOIN project_work_items i ON i.system_id=s.id
 LEFT JOIN LATERAL (
  SELECT COALESCE(SUM(e.quantity_delta),0) recorded,
   COALESCE(SUM(e.quantity_delta) FILTER (WHERE ap.event_id IS NOT NULL),0) approved,
   COUNT(e.id) FILTER (WHERE ap.event_id IS NULL) pending_events
  FROM work_assignments a JOIN work_execution_events e ON e.assignment_id=a.id
  LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id
  WHERE a.work_item_id=i.id
 ) q ON i.id IS NOT NULL
 WHERE s.revision_id=$1 GROUP BY s.id,s.code,s.name,s.sort_order ORDER BY s.sort_order,s.code`

const progressWorkersSQL = `SELECT w.display_name, COUNT(a.id)::text,
 COALESCE(SUM(ev.pending_count),0)::text,COALESCE(SUM(ev.approved_count),0)::text,
 COALESCE(SUM(ROUND(COALESCE(ev.approved_quantity,0)*r.rate_per_billable_unit,2)),0)::text
 FROM work_assignments a JOIN workers w ON w.id=a.worker_id
 JOIN worker_rate_snapshots r ON r.assignment_id=a.id
 LEFT JOIN LATERAL (
  SELECT COUNT(e.id) FILTER (WHERE ap.event_id IS NULL) pending_count,
   COUNT(e.id) FILTER (WHERE ap.event_id IS NOT NULL) approved_count,
   COALESCE(SUM(e.quantity_delta) FILTER (WHERE ap.event_id IS NOT NULL),0) approved_quantity
  FROM work_execution_events e LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id
  WHERE e.assignment_id=a.id
 ) ev ON true WHERE a.revision_id=$1
 GROUP BY w.id,w.display_name ORDER BY w.display_name,w.id`

const progressPendingSQL = `SELECT e.id::text,w.display_name,s.code,i.name_snapshot,
 e.quantity_delta::text,r.unit_code,e.performed_on::text,COALESCE(e.notes,'')
 FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id
 JOIN project_work_items i ON i.id=a.work_item_id
 JOIN project_systems s ON s.id=i.system_id
 JOIN workers w ON w.id=a.worker_id
 JOIN worker_rate_snapshots r ON r.assignment_id=a.id
 LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id
 WHERE a.revision_id=$1 AND ap.event_id IS NULL
 ORDER BY e.performed_on,e.created_at,e.id LIMIT 200`

func (x web) executionProgressGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	project, status, name, ok := x.executionAccess(w, r, u, false, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	perm, err := x.projectPermission(ctx, project, u)
	if err != nil {
		http.Error(w, "Доступ запрещён", http.StatusForbidden)
		return
	}
	d := progressPage{RevisionID: r.PathValue("id"), ProjectName: name, Status: status, CSRF: x.csrf(r, u), CanApprove: perm == "admin" || perm == "approve"}
	if err = x.loadExecutionProgress(ctx, &d); err != nil {
		http.Error(w, "Не удалось загрузить прогресс", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_ = progressTemplate.Execute(w, d)
}
func (x web) loadExecutionProgress(ctx context.Context, d *progressPage) error {
	rows, err := x.db.Query(ctx, progressSystemsSQL, d.RevisionID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v progressSystem
		if err = rows.Scan(&v.Code, &v.Name, &v.Items, &v.Complete, &v.RecordedPercent, &v.ApprovedPercent, &v.PendingEvents); err != nil {
			break
		}
		d.Systems = append(d.Systems, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = x.db.Query(ctx, progressWorkersSQL, d.RevisionID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v progressWorker
		if err = rows.Scan(&v.Name, &v.Assignments, &v.PendingEvents, &v.ApprovedEvents, &v.Accrued); err != nil {
			break
		}
		d.Workers = append(d.Workers, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if err = x.db.QueryRow(ctx, `SELECT COUNT(*)::text FROM work_execution_events e JOIN work_assignments a ON a.id=e.assignment_id LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id WHERE a.revision_id=$1 AND ap.event_id IS NULL`, d.RevisionID).Scan(&d.TotalPending); err != nil {
		return err
	}
	rows, err = x.db.Query(ctx, progressPendingSQL, d.RevisionID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v progressPending
		if err = rows.Scan(&v.ID, &v.Worker, &v.System, &v.Work, &v.Quantity, &v.Unit, &v.Date, &v.Note); err != nil {
			break
		}
		d.Pending = append(d.Pending, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return err
}

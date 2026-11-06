package httpserver

import (
	"context"
	"errors"
	"html/template"
	"math/big"
	"net/http"
	"strings"
	"time"

	"montage-ledger/internal/auth"
)

// Workload is calendar-day occupancy, not an estimate of paid hours.
type workloadDay struct{ Date, Label string }
type workloadCell struct{ Active, Conflict bool }
type workloadEntry struct {
	ID, WorkerID, Worker, System, Work, Unit, Total, Allocated, Unallocated, Recorded, Approved, Start, End, Priority string
	Overlaps                                                                                                          int
	Cells                                                                                                             []workloadCell
}
type workloadPage struct {
	RevisionID, ProjectName, Status, CSRF, Month, Prev, Next string
	CanWrite                                                 bool
	Days                                                     []workloadDay
	Entries                                                  []workloadEntry
	Assignments, Scheduled, Conflicts                        int
}

const workloadSQL = `SELECT a.id::text,w.id::text,w.display_name,s.code,i.name_snapshot,i.billable_unit_code,
 i.billable_quantity::text,COALESCE(p.planned_quantity,0)::text,
 GREATEST(0,i.billable_quantity-COALESCE(allocation.sum_quantity,0))::text,
 COALESCE(actual.recorded,0)::text,COALESCE(actual.approved,0)::text,
 COALESCE(to_char(sc.planned_start,'YYYY-MM-DD'),''),COALESCE(to_char(sc.planned_end,'YYYY-MM-DD'),''),
 COALESCE(sc.priority,'normal'),
 (SELECT count(*) FROM work_assignments a2 JOIN work_assignment_schedules sc2 ON sc2.assignment_id=a2.id
   WHERE a2.worker_id=a.worker_id AND a2.revision_id=a.revision_id AND a2.id<>a.id
   AND sc.planned_start IS NOT NULL AND sc2.planned_start<=sc.planned_end AND sc2.planned_end>=sc.planned_start)::int
 FROM work_assignments a
 JOIN project_work_items i ON i.id=a.work_item_id AND i.revision_id=a.revision_id
 JOIN project_systems s ON s.id=i.system_id
 JOIN workers w ON w.id=a.worker_id
 LEFT JOIN work_assignment_schedules sc ON sc.assignment_id=a.id
 LEFT JOIN work_assignment_allocations p ON p.assignment_id=a.id
 LEFT JOIN LATERAL (SELECT COALESCE(SUM(p2.planned_quantity),0) sum_quantity
   FROM work_assignments a2 LEFT JOIN work_assignment_allocations p2 ON p2.assignment_id=a2.id
   WHERE a2.work_item_id=i.id) allocation ON true
 LEFT JOIN LATERAL (SELECT COALESCE(SUM(e.quantity_delta),0) recorded,
   COALESCE(SUM(e.quantity_delta) FILTER (WHERE ap.event_id IS NOT NULL),0) approved
   FROM work_execution_events e LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id
   WHERE e.assignment_id=a.id) actual ON true
 WHERE a.revision_id=$1
 ORDER BY w.display_name,sc.planned_start NULLS LAST,s.code,i.sort_order,a.id
 LIMIT 1000`

const executionWorkloadHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Загрузка монтажников</title><link rel="stylesheet" href="/assets/app.css"><style>
body{font:15px/1.5 system-ui,sans-serif;background:#f5f7fa;color:#182236;margin:0}header{background:#182743;color:white;padding:18px 5%}header a{color:white}main{max-width:1500px;margin:22px auto;padding:0 16px}section{background:white;border:1px solid #dae2ec;border-radius:9px;padding:16px;margin:16px 0;overflow:auto}table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid #dde5ec;padding:8px;vertical-align:middle;text-align:left}a{color:#245ab0}input{width:115px;padding:6px;border:1px solid #bac5d3;border-radius:5px}button{background:#245ab0;color:white;border:0;border-radius:5px;padding:7px 11px;cursor:pointer}.muted{color:#66748a}.warn{color:#a12a22;font-weight:600}.timeline{width:max-content;min-width:100%;font-size:12px}.timeline th,.timeline td{text-align:center;padding:4px;min-width:19px}.timeline th:first-child,.timeline td:first-child{text-align:left;min-width:270px;position:sticky;left:0;background:white;z-index:1}.busy{background:#97c5ee}.conflict{background:#f8a58d}.bar{display:block;height:16px;border-radius:3px;background:#659fd4}.bar.conflict{background:#d76b50}form{display:flex;align-items:center;gap:6px;flex-wrap:wrap}
</style></head><body><header><a href="/app/revisions/{{.RevisionID}}/execution">Факт монтажа</a> · <a href="/app/revisions/{{.RevisionID}}/execution/schedule">Календарь</a> · <a href="/app/revisions/{{.RevisionID}}/execution/progress">Прогресс</a></header><main><h1>Загрузка монтажников и плановые объёмы</h1><p>{{.ProjectName}} · {{.Status}}</p><p class="muted">Плановые объёмы распределяются между назначениями одной монтажной позиции. Сумма не может превышать объём ведомости. Изменение плана не изменяет учёт факта, ставки и начисления. Пересечения считаются только внутри выбранной ревизии; число заданий в день не означает рабочие часы.</p><section><b>Назначений: {{.Assignments}}</b> · Со сроками: {{.Scheduled}} · <span class="warn">С пересечениями: {{.Conflicts}}</span></section>
<section><h2>Распределение объёмов</h2><table><tr><th>Монтажник / позиция</th><th>Объём позиции</th><th>План исполнителя</th><th>Осталось распределить</th><th>Факт записан / утверждён</th><th>Срок / конфликт</th><th>Изменить план</th></tr>{{range .Entries}}<tr><td><b>{{.Worker}}</b><div>{{.System}} · {{.Work}}</div></td><td>{{.Total}} {{.Unit}}</td><td>{{.Allocated}} {{.Unit}}</td><td>{{.Unallocated}} {{.Unit}}</td><td>{{.Recorded}} / {{.Approved}} {{.Unit}}</td><td>{{.Start}} — {{.End}}{{if gt .Overlaps 0}}<div class="warn">Пересечений: {{.Overlaps}}</div>{{end}}</td><td>{{if $.CanWrite}}<form method="post" action="/app/revisions/{{$.RevisionID}}/execution/{{.ID}}/allocation"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input name="quantity" inputmode="decimal" value="{{.Allocated}}" required aria-label="Плановый объём"><button>Сохранить</button></form>{{else}}Только просмотр{{end}}</td></tr>{{else}}<tr><td colspan="7">Пока нет назначений</td></tr>{{end}}</table><p class="muted">Для снятия планового объёма введи 0. Для изменения даты или приоритета открой <a href="/app/revisions/{{.RevisionID}}/execution/schedule">календарь</a>.</p></section>
<section><h2>Диаграмма Ганта — {{.Month}}</h2><p><a href="?month={{.Prev}}">← Предыдущий месяц</a> · <a href="?month={{.Next}}">Следующий месяц →</a></p><div style="overflow:auto"><table class="timeline"><thead><tr><th>Монтажник / работа</th>{{range .Days}}<th title="{{.Date}}">{{.Label}}</th>{{end}}</tr></thead><tbody>{{range .Entries}}<tr><td><b>{{.Worker}}</b> · {{.System}} · {{.Work}}</td>{{range .Cells}}<td {{if .Active}}class="{{if .Conflict}}conflict{{else}}busy{{end}}"{{end}}>{{if .Active}}<span class="bar {{if .Conflict}}conflict{{end}}" title="Запланировано"></span>{{end}}</td>{{end}}</tr>{{else}}<tr><td colspan="32">Назначений нет</td></tr>{{end}}</tbody></table></div><p class="muted">Синий — запланированное задание; красный — совпадение дат нескольких заданий одного монтажника. Совпадения не запрещены автоматически.</p></section></main></body></html>`

var executionWorkloadTemplate = template.Must(template.New("execution-workload").Parse(executionWorkloadHTML))

// Interval bounds are inclusive; a one-day overlap is still a conflict.
func workloadOverlap(aStart, aEnd, bStart, bEnd string) bool {
	return aStart != "" && aEnd != "" && bStart != "" && bEnd != "" && aStart <= bEnd && bStart <= aEnd
}

func workloadCells(start, end string, days []workloadDay, conflicting []workloadEntry, workerID, id string) []workloadCell {
	out := make([]workloadCell, 0, len(days))
	for _, day := range days {
		active := start != "" && end != "" && start <= day.Date && day.Date <= end
		clash := false
		if active {
			for _, other := range conflicting {
				if other.ID != id && other.WorkerID == workerID && other.Start != "" && other.Start <= day.Date && day.Date <= other.End {
					clash = true
					break
				}
			}
		}
		out = append(out, workloadCell{Active: active, Conflict: clash})
	}
	return out
}

func (x web) executionWorkloadGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	project, status, name, ok := x.executionAccess(w, r, u, false, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	perm, err := x.projectPermission(ctx, project, u)
	if err != nil {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	month, err := scheduleMonth(r.URL.Query().Get("month"), time.Now())
	if err != nil {
		http.Error(w, "Неверный месяц", 400)
		return
	}
	d := workloadPage{RevisionID: r.PathValue("id"), ProjectName: name, Status: status, CSRF: x.csrf(r, u), Month: month.Format("2006-01"), Prev: month.AddDate(0, -1, 0).Format("2006-01"), Next: month.AddDate(0, 1, 0).Format("2006-01"), CanWrite: canEdit(perm) && status != "archived"}
	for date := month; date.Month() == month.Month(); date = date.AddDate(0, 0, 1) {
		d.Days = append(d.Days, workloadDay{Date: date.Format("2006-01-02"), Label: date.Format("02")})
	}
	rows, err := x.db.Query(ctx, workloadSQL, d.RevisionID)
	if err != nil {
		http.Error(w, "Не удалось загрузить загрузку", 500)
		return
	}
	for rows.Next() {
		var e workloadEntry
		if err = rows.Scan(&e.ID, &e.WorkerID, &e.Worker, &e.System, &e.Work, &e.Unit, &e.Total, &e.Allocated, &e.Unallocated, &e.Recorded, &e.Approved, &e.Start, &e.End, &e.Priority, &e.Overlaps); err != nil {
			break
		}
		d.Entries = append(d.Entries, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Не удалось прочитать задания", 500)
		return
	}
	d.Assignments = len(d.Entries)
	for i := range d.Entries {
		e := &d.Entries[i]
		if e.Start != "" {
			d.Scheduled++
		}
		if e.Overlaps > 0 {
			d.Conflicts++
		}
		e.Cells = workloadCells(e.Start, e.End, d.Days, d.Entries, e.WorkerID, e.ID)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_ = executionWorkloadTemplate.Execute(w, d)
}

// An allocation only changes a plan. PostgreSQL trigger locks the work-item
// row and validates the combined allocation even under concurrent requests.
func (x web) executionAllocationSave(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	_, status, _, ok := x.executionAccess(w, r, u, true, false)
	if !ok {
		return
	}
	if !x.validPost(w, r, u) {
		return
	}
	if status == "archived" {
		http.Error(w, "Архивная ревизия недоступна", 409)
		return
	}
	assignment := r.PathValue("assignment")
	if !uuidPattern.MatchString(assignment) {
		http.Error(w, "Неверное назначение", 400)
		return
	}
	raw := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("quantity"), ",", "."))
	num, err := parseAllocation(raw)
	if err != nil {
		http.Error(w, "Неверный плановый объём: от 0 до 999999999999.99999999, не более 8 знаков после точки", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tag, err := x.db.Exec(ctx, `INSERT INTO work_assignment_allocations(assignment_id,planned_quantity,updated_by)
 SELECT a.id,$3::numeric,$4 FROM work_assignments a JOIN project_revisions rev ON rev.id=a.revision_id
 WHERE a.id=$1 AND a.revision_id=$2 AND rev.status<>'archived'
 ON CONFLICT(assignment_id) DO UPDATE SET planned_quantity=EXCLUDED.planned_quantity,updated_by=EXCLUDED.updated_by,updated_at=now()`, assignment, r.PathValue("id"), num, u.ID)
	if err != nil {
		http.Error(w, "Нельзя распределить объём: проверьте сумму планов по позиции", 409)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "Назначение не найдено", 404)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+r.PathValue("id")+"/execution/workload", http.StatusSeeOther)
}

func parseAllocation(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "eE+ ") || strings.HasPrefix(raw, "-") {
		return "", errors.New("invalid syntax")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || len(parts[0]) > 12 || len(parts[0]) == 0 || (len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > 8)) {
		return "", errors.New("invalid precision")
	}
	for _, c := range raw {
		if (c < '0' || c > '9') && c != '.' {
			return "", errors.New("invalid character")
		}
	}
	n, ok := new(big.Rat).SetString(raw)
	if !ok || n.Sign() < 0 {
		return "", errors.New("invalid number")
	}
	return n.FloatString(8), nil
}

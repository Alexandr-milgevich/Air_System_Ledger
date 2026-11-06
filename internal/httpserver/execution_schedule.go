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

// Scheduling is operational metadata; it never edits an assignment's rate or execution history.
type scheduleEntry struct {
	ID, System, Work, Worker, Unit, Planned, Recorded, Approved, Start, End, Priority, Notes, State string
	Overdue                                                                                         bool
}
type scheduleDay struct {
	Date, Label string
	Entries     []scheduleEntry
}
type schedulePage struct {
	RevisionID, ProjectName, RevisionStatus, CSRF, Month, Prev, Next string
	CanWrite                                                         bool
	Entries                                                          []scheduleEntry
	Days                                                             []scheduleDay
	Unplanned, Overdue, InProgress, Done                             int
}

const executionScheduleHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Календарь монтажных заданий</title><link rel="stylesheet" href="/assets/app.css"><style>body{font:15px/1.5 system-ui,sans-serif;background:#f5f7fa;color:#182236;margin:0}header{background:#182743;color:white;padding:18px 5%}header a{color:white}main{max-width:1350px;margin:24px auto;padding:0 18px}section{background:white;border:1px solid #dae2ec;border-radius:10px;padding:18px;margin:18px 0;overflow:auto}table{width:100%;border-collapse:collapse}td,th{border-bottom:1px solid #dfe5ed;padding:9px;text-align:left;vertical-align:top}input,select{padding:6px;border:1px solid #bdc8d9;border-radius:5px;box-sizing:border-box}input[type=date]{width:145px}input[name=notes]{min-width:185px;width:100%}button{padding:8px 12px;background:#245ab0;border:0;border-radius:5px;color:white;cursor:pointer}a{color:#245ab0}.muted{color:#65758a}.overdue{color:#a42626;font-weight:600}.calendar{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:6px}.day{border:1px solid #dfe5ed;padding:8px;min-height:75px;word-break:break-word;border-radius:6px}.day strong{display:block;margin-bottom:6px}.chip{font-size:12px;display:block;background:#eef3fa;padding:4px;margin:3px 0;border-radius:4px}.toolbar{display:flex;gap:16px;align-items:center}.scroll{overflow-x:auto}</style></head><body><header><a href="/app/revisions/{{.RevisionID}}/execution">Факт монтажа</a> · <a href="/app/revisions/{{.RevisionID}}/execution/progress">Прогресс</a> · <a href="/app/revisions/{{.RevisionID}}/ledger">Ведомость</a> · <a href="/app/revisions/{{.RevisionID}}/execution/workload">Загрузка монтажников</a></header><main><h1>Календарное планирование монтажных работ</h1><p>{{.ProjectName}} · {{.RevisionStatus}}</p><p class="muted">Статусы рассчитываются из записанного факта. Просрочка определяется по дате окончания, а «Выполнено» — по утверждённому объёму. Планирование не изменяет начисления.</p><section><h2>Сводка</h2><p>Без сроков: <b>{{.Unplanned}}</b> · Просрочено: <b>{{.Overdue}}</b> · В работе: <b>{{.InProgress}}</b> · Завершено: <b>{{.Done}}</b></p></section>
<section><div class="toolbar"><a href="?month={{.Prev}}">← Предыдущий месяц</a><h2>{{.Month}}</h2><a href="?month={{.Next}}">Следующий месяц →</a></div><p class="muted">В календаре показываются задания по плановой дате завершения. Полный список — ниже.</p><div class="calendar">{{range .Days}}<div class="day"><strong>{{.Label}}</strong>{{range .Entries}}<span class="chip">{{.System}} · {{.Worker}}<span class="muted"> {{.State}}</span></span>{{end}}</div>{{end}}</div></section>
<section class="scroll"><h2>Назначения и сроки</h2><table><tr><th>Система / работа</th><th>Монтажник</th><th>План</th><th>Записано</th><th>Утверждено</th><th>Статус</th><th>Период / приоритет / примечание</th></tr>{{range .Entries}}<tr><td><b>{{.System}}</b> · {{.Work}}</td><td>{{.Worker}}</td><td>{{.Planned}} {{.Unit}}</td><td>{{.Recorded}} {{.Unit}}</td><td>{{.Approved}} {{.Unit}}</td><td>{{if .Overdue}}<span class="overdue">{{.State}}</span>{{else}}{{.State}}{{end}}</td><td>{{if $.CanWrite}}<form method="post" action="/app/revisions/{{$.RevisionID}}/execution/{{.ID}}/schedule"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="date" name="start" value="{{.Start}}" required aria-label="Начало"><input type="date" name="end" value="{{.End}}" required aria-label="Окончание"><select name="priority" aria-label="Приоритет"><option value="normal" {{if eq .Priority "normal"}}selected{{end}}>Обычный</option><option value="low" {{if eq .Priority "low"}}selected{{end}}>Низкий</option><option value="high" {{if eq .Priority "high"}}selected{{end}}>Высокий</option><option value="critical" {{if eq .Priority "critical"}}selected{{end}}>Критический</option></select><input name="notes" maxlength="500" value="{{.Notes}}" placeholder="Примечание" aria-label="Примечание"><button>Сохранить</button></form>{{else}}{{.Start}} — {{.End}} · {{.Priority}} · {{.Notes}}{{end}}</td></tr>{{else}}<tr><td colspan="7">Монтажники ещё не назначены</td></tr>{{end}}</table></section></main></body></html>`

var executionScheduleTemplate = template.Must(template.New("execution-schedule").Parse(executionScheduleHTML))

const executionScheduleSQL = `SELECT a.id::text,s.code,i.name_snapshot,w.display_name,
 i.billable_unit_code,i.billable_quantity::text,
 COALESCE(f.recorded,0)::text,COALESCE(f.approved,0)::text,
 COALESCE(to_char(sc.planned_start,'YYYY-MM-DD'),''),COALESCE(to_char(sc.planned_end,'YYYY-MM-DD'),''),
 COALESCE(sc.priority,'normal'),COALESCE(sc.notes,'')
 FROM work_assignments a
 JOIN project_work_items i ON i.id=a.work_item_id AND i.revision_id=a.revision_id
 JOIN project_systems s ON s.id=i.system_id
 JOIN workers w ON w.id=a.worker_id
 LEFT JOIN work_assignment_schedules sc ON sc.assignment_id=a.id
 LEFT JOIN LATERAL (
  SELECT COALESCE(SUM(e.quantity_delta),0) recorded,
   COALESCE(SUM(e.quantity_delta) FILTER (WHERE ap.event_id IS NOT NULL),0) approved
  FROM work_execution_events e LEFT JOIN work_execution_approvals ap ON ap.event_id=e.id
  WHERE e.assignment_id=a.id
 ) f ON true
 WHERE a.revision_id=$1
 ORDER BY sc.planned_end NULLS LAST,s.sort_order,i.sort_order,w.display_name,a.id`

func scheduleMonth(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	}
	if len(raw) != 7 || raw[4] != '-' {
		return time.Time{}, errors.New("invalid month")
	}
	t, e := time.Parse("2006-01", raw)
	if e != nil || t.Year() < 2000 || t.Year() > 2100 {
		return time.Time{}, errors.New("invalid month")
	}
	return t, nil
}

func scheduleRat(s string) *big.Rat {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil
	}
	return v
}

func scheduleState(planned, recorded, approved, finish string, today time.Time) (string, bool) {
	// PostgreSQL fixed-scale NUMERIC values are validated; use a rational comparison without floats.
	p := scheduleRat(planned)
	r := scheduleRat(recorded)
	a := scheduleRat(approved)
	if p != nil && p.Sign() > 0 && a != nil && a.Cmp(p) >= 0 {
		return "Выполнено", false
	}
	if finish != "" {
		if _, err := time.Parse("2006-01-02", finish); err == nil && finish < today.Format("2006-01-02") {
			return "Просрочено", true
		}
	}
	if r != nil && r.Sign() > 0 {
		return "В работе", false
	}
	if finish == "" {
		return "Без сроков", false
	}
	return "Запланировано", false
}

func (x web) executionScheduleGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
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
	d := schedulePage{RevisionID: r.PathValue("id"), ProjectName: name, RevisionStatus: status, CSRF: x.csrf(r, u), CanWrite: canEdit(perm) && status != "archived", Month: month.Format("2006-01"), Prev: month.AddDate(0, -1, 0).Format("2006-01"), Next: month.AddDate(0, 1, 0).Format("2006-01")}
	rows, err := x.db.Query(ctx, executionScheduleSQL, d.RevisionID)
	if err != nil {
		http.Error(w, "Не удалось загрузить план", 500)
		return
	}
	today := time.Now()
	for rows.Next() {
		var v scheduleEntry
		if err = rows.Scan(&v.ID, &v.System, &v.Work, &v.Worker, &v.Unit, &v.Planned, &v.Recorded, &v.Approved, &v.Start, &v.End, &v.Priority, &v.Notes); err != nil {
			break
		}
		v.State, v.Overdue = scheduleState(v.Planned, v.Recorded, v.Approved, v.End, today)
		switch {
		case v.Overdue:
			d.Overdue++
		case v.State == "Выполнено":
			d.Done++
		case v.State == "В работе":
			d.InProgress++
		case v.End == "":
			d.Unplanned++
		}
		d.Entries = append(d.Entries, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Не удалось прочитать план", 500)
		return
	}
	// Seven-column calendar; include blank cells to align weekdays (Monday first).
	offset := (int(month.Weekday()) + 6) % 7
	for j := 0; j < offset; j++ {
		d.Days = append(d.Days, scheduleDay{})
	}
	for day := month; day.Month() == month.Month(); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		c := scheduleDay{Date: date, Label: day.Format("02")}
		for _, v := range d.Entries {
			if v.End == date {
				c.Entries = append(c.Entries, v)
			}
		}
		d.Days = append(d.Days, c)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	_ = executionScheduleTemplate.Execute(w, d)
}

func (x web) executionScheduleSave(w http.ResponseWriter, r *http.Request, u auth.Identity) {
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
	start, err := time.Parse("2006-01-02", r.PostFormValue("start"))
	if err != nil {
		http.Error(w, "Неверная дата начала", 400)
		return
	}
	end, err := time.Parse("2006-01-02", r.PostFormValue("end"))
	if err != nil || end.Before(start) || start.Year() < 2000 || end.Year() > 2100 {
		http.Error(w, "Неверные сроки", 400)
		return
	}
	priority := r.PostFormValue("priority")
	if priority != "low" && priority != "normal" && priority != "high" && priority != "critical" {
		http.Error(w, "Неверный приоритет", 400)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("notes"))
	if len([]rune(note)) > 500 {
		http.Error(w, "Примечание слишком длинное", 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	// An atomic INSERT..SELECT enforces that assignment belongs to this revision.
	tag, err := x.db.Exec(ctx, `INSERT INTO work_assignment_schedules(assignment_id,planned_start,planned_end,priority,notes,updated_by)
  SELECT a.id,$3::date,$4::date,$5,$6,$7 FROM work_assignments a JOIN project_revisions rev ON rev.id=a.revision_id
  WHERE a.id=$1 AND a.revision_id=$2 AND rev.status<>'archived'
  ON CONFLICT(assignment_id) DO UPDATE SET planned_start=EXCLUDED.planned_start,planned_end=EXCLUDED.planned_end,
  priority=EXCLUDED.priority,notes=EXCLUDED.notes,updated_by=EXCLUDED.updated_by,updated_at=now()`, assignment, r.PathValue("id"), start.Format("2006-01-02"), end.Format("2006-01-02"), priority, note, u.ID)
	if err != nil {
		http.Error(w, "Не удалось сохранить расписание", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "Назначение не найдено", 404)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+r.PathValue("id")+"/execution/schedule", http.StatusSeeOther)
}

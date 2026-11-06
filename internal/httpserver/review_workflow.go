package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"montage-ledger/internal/auth"
	"montage-ledger/internal/xlsxledger"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
)

type reviewEvent struct{ From, To, Actor, Comment, When string }
type reviewAmountEvent struct{ Action, System, Name, Actor, Reason, When string }
type reviewPage struct {
	Revision, Project, Status, CSRF             string
	CanSubmit, CanDecide, CanRequest, CanAccept bool
	Events                                      []reviewEvent
	AmountEvents                                []reviewAmountEvent
	Checks                                      reviewPreflight
}

var reviewTemplate = template.Must(template.New("review").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Согласование ведомости</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:900px;margin:2em auto;padding:1em;font:16px system-ui"><p><a href="/app/revisions/{{.Revision}}">← К ревизии</a></p><h1>Согласование ведомости</h1><p>Статус: <strong>{{.Status}}</strong></p>{{if .CanSubmit}}<form action="/app/revisions/{{.Revision}}/review/submit" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Передать на проверку</button></form>{{end}}{{if .CanDecide}}<form action="/app/revisions/{{.Revision}}/review/decide" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label for="comment">Комментарий (обязателен для возврата)</label><textarea name="comment" id="comment" maxlength="2000" rows="3" style="display:block;width:100%"></textarea><button type="submit" name="decision" value="return">Вернуть на доработку</button> <button type="submit" name="decision" value="approve">Утвердить</button></form>{{end}}{{if eq .Status "approved"}}<p><a href="/app/revisions/{{.Revision}}/review/approved.xlsx">Скачать утверждённый XLSX</a></p>{{end}}{{if or (eq .Status "draft") (eq .Status "in_review")}}<section class="review-check" style="background:#fff;border:1px solid #dce3eb;border-radius:10px;padding:16px;margin:16px 0"><h2>Проверка перед согласованием</h2>{{if and (eq .Status "in_review") .Checks.HasPending}}<p style="color:#9c1e1e">Ожидаются решения согласующего по историческим суммам. Утверждение пока недоступно.</p>{{else if .Checks.Ready}}<p>✓ Расхождения отсутствуют либо оформлены разрешёнными исключениями. Возможны другие ограничения при смене статуса.</p>{{else}}<p style="color:#9c1e1e">Есть причины, препятствующие согласованию. Исправьте их в <a href="/app/revisions/{{.Revision}}/ledger">редакторе ведомости</a> и обновите страницу.</p>{{end}}{{if not .Checks.HasSystems}}<p>Нет ни одной инженерной системы.</p>{{end}}{{if not .Checks.HasItems}}<p>Нет ни одной монтажной работы.</p>{{end}}{{if .Checks.Issues}}<div style="overflow-x:auto"><table><thead><tr><th>Система</th><th>Работа</th><th>Оплачиваемое количество</th><th>Расценка</th><th>В ведомости</th><th>По формуле</th><th>Отклонение</th><th>Исключение</th></tr></thead><tbody>{{range .Checks.Issues}}<tr><td>{{.System}}</td><td>{{.Name}}</td><td>{{.Quantity}}</td><td>{{.Price}}</td><td>{{.Saved}}</td><td>{{.Expected}}</td><td>{{.Difference}}</td><td>{{.ExceptionStatus}}{{if .Reason}}<p><small>{{.Reason}}</small></p>{{end}}{{if and $.CanRequest .Legacy}}<form method="post" action="/app/revisions/{{$.Revision}}/review/exception/request"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="item" value="{{.ID}}"><label>Обоснование исключения</label><textarea name="reason" minlength="15" maxlength="2000" rows="2" required></textarea><button type="submit">Запросить исключение</button></form>{{end}}{{if and $.CanAccept .Pending}}<form method="post" action="/app/revisions/{{$.Revision}}/review/exception/accept"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="item" value="{{.ID}}"><button type="submit">Подтвердить исключение</button></form>{{end}}</td></tr>{{end}}</tbody></table></div>{{end}}{{if .Checks.Truncated}}<p>Показаны первые 100 расхождений. После исправления обновите проверку.</p>{{end}}<p><small>Проверка: сумма = округление (оплачиваемое количество × расценка) до 2 знаков, допустимое отклонение ≤ 0,01 ₽. Исторические суммы XLSX автоматически не исправляются.</small></p></section>{{end}}<h2>История решений</h2><table><tr><th>Время</th><th>Переход</th><th>Автор</th><th>Комментарий</th></tr>{{range .Events}}<tr><td>{{.When}}</td><td>{{.From}} → {{.To}}</td><td>{{.Actor}}</td><td>{{.Comment}}</td></tr>{{else}}<tr><td colspan="4">Решений пока нет</td></tr>{{end}}</table><h2>История исключений стоимости</h2><table><tr><th>Время</th><th>Система / работа</th><th>Действие</th><th>Автор</th><th>Обоснование</th></tr>{{range .AmountEvents}}<tr><td>{{.When}}</td><td>{{.System}} · {{.Name}}</td><td>{{.Action}}</td><td>{{.Actor}}</td><td>{{.Reason}}</td></tr>{{else}}<tr><td colspan="5">Исключений пока нет</td></tr>{{end}}</table></main></body></html>`))

func allowedReviewAction(action, status, permission string) bool {
	switch action {
	case "submit":
		return status == "draft" && canEdit(permission)
	case "return", "approve":
		return status == "in_review" && (permission == "approve" || permission == "admin")
	}
	return false
}

func (x web) reviewGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	revision := paramID(w, r)
	if revision == "" {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	project, status, _, err := x.revisionAccess(ctx, revision, u)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	permission, err := x.projectPermission(ctx, project, u)
	if err != nil {
		http.Error(w, "Доступ запрещён", http.StatusForbidden)
		return
	}
	data := reviewPage{Revision: revision, Project: project, Status: status, CSRF: x.csrf(r, u), CanSubmit: allowedReviewAction("submit", status, permission), CanDecide: allowedReviewAction("approve", status, permission), CanRequest: status == "draft" && canEdit(permission), CanAccept: status == "in_review" && (permission == "approve" || permission == "admin")}
	if status == "draft" || status == "in_review" {
		data.Checks, err = loadReviewPreflight(ctx, x.db, revision)
		if err != nil {
			slog.Error("review preflight failed", "revision", revision, "error", err)
			http.Error(w, "Ошибка проверки ведомости", http.StatusInternalServerError)
			return
		}
	}
	rows, err := x.db.Query(ctx, `SELECT e.from_status,e.to_status,COALESCE(us.display_name,'Система'),e.comment,to_char(e.created_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS "UTC"') FROM revision_review_events e LEFT JOIN users us ON us.id=e.actor_id WHERE e.revision_id=$1 ORDER BY e.id DESC LIMIT 100`, revision)
	if err != nil {
		http.Error(w, "Ошибка истории", 500)
		return
	}
	for rows.Next() {
		var ev reviewEvent
		if err = rows.Scan(&ev.From, &ev.To, &ev.Actor, &ev.Comment, &ev.When); err != nil {
			break
		}
		data.Events = append(data.Events, ev)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка истории", 500)
		return
	}
	amountRows, err := x.db.Query(ctx, `SELECT
        e.action, COALESCE(s.code,'—'), COALESCE(i.name_snapshot, COALESCE(e.calculation_snapshot->>'name','—')),
        COALESCE(u.display_name,'Система'),e.reason,
        to_char(e.created_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS "UTC"')
        FROM review_amount_exception_events e
        LEFT JOIN project_work_items i ON i.id=e.work_item_id AND i.revision_id=e.revision_id
        LEFT JOIN project_systems s ON s.id=i.system_id
        LEFT JOIN users u ON u.id=e.actor_id
        WHERE e.revision_id=$1 ORDER BY e.id DESC LIMIT 100`, revision)
	if err != nil {
		http.Error(w, "Ошибка истории исключений", 500)
		return
	}
	for amountRows.Next() {
		var ev reviewAmountEvent
		if err = amountRows.Scan(&ev.Action, &ev.System, &ev.Name, &ev.Actor, &ev.Reason, &ev.When); err != nil {
			break
		}
		data.AmountEvents = append(data.AmountEvents, ev)
	}
	if err == nil {
		err = amountRows.Err()
	}
	amountRows.Close()
	if err != nil {
		http.Error(w, "Ошибка истории исключений", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if err = reviewTemplate.Execute(w, data); err != nil {
		slog.Error("review render failed", "err", err)
	}
}

func (x web) reviewSubmit(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.reviewTransition(w, r, u, "submit")
}
func (x web) reviewDecide(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	if !x.validPost(w, r, u) {
		return
	}
	decision := r.PostFormValue("decision")
	if decision != "approve" && decision != "return" {
		http.Error(w, "Некорректное решение", 400)
		return
	}
	x.reviewTransitionValidated(w, r, u, decision)
}
func (x web) reviewTransition(w http.ResponseWriter, r *http.Request, u auth.Identity, action string) {
	if !x.validPost(w, r, u) {
		return
	}
	x.reviewTransitionValidated(w, r, u, action)
}
func (x web) reviewTransitionValidated(w http.ResponseWriter, r *http.Request, u auth.Identity, action string) {
	revision := paramID(w, r)
	if revision == "" {
		return
	}
	comment := strings.TrimSpace(r.PostFormValue("comment"))
	if len([]rune(comment)) > 2000 || (action == "return" && comment == "") {
		http.Error(w, "Для возврата требуется комментарий (до 2000 символов)", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tx, err := x.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	// Row lock must precede validation and status update, including simultaneous approvals.
	var project, status string
	err = tx.QueryRow(ctx, `SELECT project_id::text,status FROM project_revisions WHERE id=$1 FOR UPDATE`, revision).Scan(&project, &status)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	permission, err := x.projectPermission(ctx, project, u)
	if err != nil || !allowedReviewAction(action, status, permission) {
		http.Error(w, "Операция недоступна для статуса или роли", 403)
		return
	}
	if action == "submit" || action == "approve" {
		// Do not approve empty/incomplete estimates. Missing system, positions, invalid values.
		var systems, items int
		err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_systems WHERE revision_id=$1), (SELECT count(*) FROM project_work_items WHERE revision_id=$1)`, revision).Scan(&systems, &items)
		if err != nil {
			http.Error(w, "Ошибка проверки ведомости", 500)
			return
		}
		if systems == 0 || items == 0 {
			http.Error(w, "Для согласования нужны система и хотя бы одна работа", 409)
			return
		}
		// All outstanding discrepancies must be explained by an exact historical
		// snapshot. A request permits submission, but only reviewer acceptance
		// permits final approval. Verified again under the revision row lock.
		invalid, checkErr := outstandingReviewAmountCount(ctx, tx, revision, action == "approve")
		if checkErr != nil {
			slog.Error("review exception validation failed", "error", checkErr)
			http.Error(w, "Ошибка проверки расчётов и исключений", 500)
			return
		}
		if invalid > 0 {
			http.Error(w, "Есть необоснованные или неподтверждённые расхождения; откройте диагностику согласования", 409)
			return
		}
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true),set_config('app.workflow_comment',$2,true)`, u.ID, comment); err != nil {
		http.Error(w, "Ошибка аудита", 500)
		return
	}
	target := "in_review"
	switch action {
	case "approve":
		target = "approved"
	case "return":
		target = "draft"
	}
	if target == "approved" {
		_, err = tx.Exec(ctx, `UPDATE project_revisions SET status='approved',approved_at=clock_timestamp(),approved_by=$2 WHERE id=$1`, revision, u.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE project_revisions SET status=$2 WHERE id=$1`, revision, target)
	}
	if err != nil {
		slog.Error("review transition failed", "error", err)
		http.Error(w, "Не удалось изменить статус", 409)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		http.Error(w, "Не удалось сохранить решение", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+revision+"/review", http.StatusSeeOther)
}

// An approved export includes a separate metadata sheet, leaving all ledger
// quantities and price snapshots intact.
func (x web) approvedXLSX(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	revision := paramID(w, r)
	if revision == "" {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, status, _, err := x.revisionAccess(ctx, revision, u)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if status != "approved" {
		http.Error(w, "Ведомость ещё не утверждена", 409)
		return
	}
	var number int
	var approvedAt, approvedBy string
	err = x.db.QueryRow(ctx, `SELECT r.number,to_char(r.approved_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS "UTC"'),COALESCE(us.display_name,'') FROM project_revisions r LEFT JOIN users us ON us.id=r.approved_by WHERE r.id=$1`, revision).Scan(&number, &approvedAt, &approvedBy)
	if err != nil {
		http.Error(w, "Ошибка метаданных", 500)
		return
	}
	rows, err := x.db.Query(ctx, `SELECT s.code,s.name,i.name_snapshot,i.input_unit_code,i.input_quantity::text,COALESCE(i.coefficient::text,''),i.calculated_quantity::text,i.unit_price::text,i.amount::text,COALESCE(l.name,'') FROM project_work_items i JOIN project_systems s ON s.id=i.system_id AND s.revision_id=i.revision_id LEFT JOIN project_locations l ON l.id=i.location_id AND l.revision_id=i.revision_id WHERE i.revision_id=$1 ORDER BY s.sort_order,s.code,i.sort_order,i.id`, revision)
	if err != nil {
		http.Error(w, "Ошибка данных", 500)
		return
	}
	var items []xlsxledger.Item
	for rows.Next() {
		var it xlsxledger.Item
		if err = rows.Scan(&it.SystemCode, &it.SystemName, &it.Name, &it.Unit, &it.Quantity, &it.Coefficient, &it.Calculated, &it.Price, &it.Amount, &it.Location); err != nil {
			break
		}
		items = append(items, it)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка данных", 500)
		return
	}
	var buf bytes.Buffer
	if err = xlsxledger.Export(&buf, items); err != nil {
		http.Error(w, "Ошибка формирования XLSX", 500)
		return
	}
	book, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		http.Error(w, "Ошибка XLSX", 500)
		return
	}
	defer book.Close()
	sheet := "Согласование"
	if _, err = book.NewSheet(sheet); err != nil {
		http.Error(w, "Ошибка листа", 500)
		return
	}
	cells := map[string]any{"A1": "Утверждённая монтажная ведомость", "A3": "Номер ревизии", "B3": number, "A4": "Идентификатор ревизии", "B4": revision, "A5": "Статус", "B5": "Утверждено", "A6": "Утвердил", "B6": approvedBy, "A7": "Дата (UTC)", "B7": approvedAt}
	for cell, value := range cells {
		if err = book.SetCellValue(sheet, cell, value); err != nil {
			http.Error(w, "Ошибка оформления XLSX", 500)
			return
		}
	}
	_ = book.SetColWidth(sheet, "A", "A", 30)
	_ = book.SetColWidth(sheet, "B", "B", 42)
	content, err := book.WriteToBuffer()
	if err != nil {
		http.Error(w, "Ошибка экспорта", 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="approved-revision-%d.xlsx"`, number))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(content.Bytes())
}

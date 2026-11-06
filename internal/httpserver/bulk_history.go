package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"montage-ledger/internal/auth"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// revisionFingerprint includes every row, not only the items changed by the bulk operation.
// This deliberately prevents undo if ANY subsequent ledger edit was made.
func revisionFingerprint(ctx context.Context, tx pgx.Tx, revision string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, row_to_json(t)::text FROM (
 SELECT id, revision_id, system_id, location_id, work_catalog_id, name_snapshot,
 source_type, input_quantity, input_unit_code, coefficient, calculated_quantity,
 result_unit_code, billable_quantity, billable_unit_code, unit_price, amount,
 currency_code, rule_snapshot, inputs_snapshot, trace_snapshot, manually_modified,
 notes, sort_order FROM project_work_items WHERE revision_id=$1 ORDER BY id
 ) AS t`, revision)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var id, jsonText string
		if err = rows.Scan(&id, &jsonText); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s%d:%s;", len(id), id, len(jsonText), jsonText)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type bulkHistoryRecord struct {
	ID, Action, Value, Actor, CreatedAt, Before, After string
	Reverted                                           bool
	Items                                              int
}
type bulkHistoryView struct {
	RevisionID, CSRF string
	CanWrite         bool
	Records          []bulkHistoryRecord
}

const bulkHistoryHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>История массовых изменений</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:1100px;margin:32px auto;padding:18px"><h1>История массовых изменений</h1><p><a href="/app/revisions/{{.RevisionID}}/ledger">Вернуться в редактор</a></p><p>Откат доступен только для самой последней операции в черновой ревизии, если после неё ведомость не изменялась. Отмена изменений — отдельное аудируемое действие.</p><div style="overflow-x:auto"><table style="width:100%"><thead><tr><th>Время (UTC)</th><th>Автор</th><th>Операция</th><th>Строк</th><th>До, ₽</th><th>После, ₽</th><th>Статус</th><th>Откат</th></tr></thead><tbody>{{range $i,$r:=.Records}}<tr><td>{{$r.CreatedAt}}</td><td>{{$r.Actor}}</td><td>{{$r.Action}} {{$r.Value}}</td><td>{{$r.Items}}</td><td>{{$r.Before}}</td><td>{{$r.After}}</td><td>{{if $r.Reverted}}Отменена{{else}}Выполнена{{end}}</td><td>{{if and $.CanWrite (eq $i 0) (not $r.Reverted)}}<form method="post" action="/app/revisions/{{$.RevisionID}}/bulk/{{ $r.ID }}/undo"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit">Откатить</button></form>{{else}}—{{end}}</td></tr>{{else}}<tr><td colspan="8">Пока нет массовых изменений</td></tr>{{end}}</tbody></table></div></main></body></html>`

var bulkHistoryTemplate = template.Must(template.New("bulkHistory").Parse(bulkHistoryHTML))

func (x web) bulkHistoryGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	revision := r.PathValue("id")
	if !uuidPattern.MatchString(revision) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	var project, status, permission string
	err := x.db.QueryRow(ctx, `SELECT project_id::text,status FROM project_revisions WHERE id=$1`, revision).Scan(&project, &status)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	permission, err = x.projectPermission(ctx, project, u)
	if err != nil || permission == "" {
		http.Error(w, "Доступ запрещён", http.StatusForbidden)
		return
	}
	canWrite := status == "draft" && (permission == "write" || permission == "admin" || permission == "approve")
	rs, err := x.db.Query(ctx, `SELECT o.id::text,o.action,o.value,COALESCE(u.display_name,'Пользователь удалён'),o.created_at,o.before_total::text,o.after_total::text,o.reverted_at IS NOT NULL,(SELECT count(*)::int FROM bulk_change_items bi WHERE bi.operation_id=o.id) FROM bulk_change_operations o LEFT JOIN users u ON u.id=o.actor_id WHERE o.revision_id=$1 ORDER BY o.created_at DESC,o.id DESC LIMIT 100`, revision)
	if err != nil {
		slog.Error("bulk history query failed", "error", err)
		http.Error(w, "Ошибка истории", 500)
		return
	}
	defer rs.Close()
	data := bulkHistoryView{RevisionID: revision, CSRF: x.csrf(r, u), CanWrite: canWrite}
	for rs.Next() {
		var a bulkHistoryRecord
		var created time.Time
		if err = rs.Scan(&a.ID, &a.Action, &a.Value, &a.Actor, &created, &a.Before, &a.After, &a.Reverted, &a.Items); err != nil {
			break
		}
		a.CreatedAt = created.UTC().Format("2006-01-02 15:04:05")
		data.Records = append(data.Records, a)
	}
	if err == nil {
		err = rs.Err()
	}
	if err != nil {
		slog.Error("bulk history scan failed", "error", err)
		http.Error(w, "Ошибка истории", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err = bulkHistoryTemplate.Execute(w, data); err != nil {
		slog.Error("bulk history template failed", "error", err)
	}
}

func (x web) bulkUndo(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	revision, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	opID := r.PathValue("operation")
	if !uuidPattern.MatchString(opID) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Ошибка транзакции", 500)
		return
	}
	defer tx.Rollback(ctx)
	if _, auditErr := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, u.ID); auditErr != nil {
		http.Error(w, "Ошибка контекста аудита", 500)
		return
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 FOR UPDATE`, revision).Scan(&status)
	if err != nil || status != "draft" {
		http.Error(w, "Ревизия закрыта", 409)
		return
	}
	// Never undo an operation older than another operation, even if the newer one was reverted.
	var latestID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM bulk_change_operations WHERE revision_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, revision).Scan(&latestID)
	if err != nil || latestID != opID {
		http.Error(w, "Можно отменить только последнюю массовую операцию", 409)
		return
	}
	var postHash string
	var alreadyReverted bool
	err = tx.QueryRow(ctx, `SELECT post_revision_hash,reverted_at IS NOT NULL FROM bulk_change_operations WHERE id=$1 AND revision_id=$2 FOR UPDATE`, opID, revision).Scan(&postHash, &alreadyReverted)
	if err != nil || alreadyReverted {
		http.Error(w, "Операция уже отменена или отсутствует", 409)
		return
	}
	digest, err := revisionFingerprint(ctx, tx, revision)
	if err != nil {
		http.Error(w, "Ошибка проверки ревизии", 500)
		return
	}
	if digest != postHash {
		http.Error(w, "Ведомость изменена после массовой операции. Автоматический откат запрещён", 409)
		return
	}
	rs, err := tx.Query(ctx, `SELECT item_id::text,old_price::text,old_amount::text,COALESCE(old_location_id::text,''),old_manually_modified FROM bulk_change_items WHERE operation_id=$1 ORDER BY item_id`, opID)
	if err != nil {
		http.Error(w, "Ошибка чтения истории", 500)
		return
	}
	type snapshot struct {
		ID, Price, Amount, Location string
		Modified                    bool
	}
	var snapshots []snapshot
	for rs.Next() {
		var s snapshot
		if err = rs.Scan(&s.ID, &s.Price, &s.Amount, &s.Location, &s.Modified); err != nil {
			break
		}
		snapshots = append(snapshots, s)
	}
	if err == nil {
		err = rs.Err()
	}
	rs.Close()
	if err != nil || len(snapshots) == 0 {
		http.Error(w, "Ошибка данных для отката", 500)
		return
	}
	for _, s := range snapshots {
		var loc any
		if s.Location != "" {
			loc = s.Location
		}
		tag, e := tx.Exec(ctx, `UPDATE project_work_items SET unit_price=$3,amount=$4,location_id=$5,manually_modified=$6 WHERE revision_id=$1 AND id=$2`, revision, s.ID, s.Price, s.Amount, loc, s.Modified)
		if e != nil || tag.RowsAffected() != 1 {
			slog.Error("bulk undo update failed", "error", e)
			http.Error(w, "Откат отменён", 409)
			return
		}
	}
	if err = refreshDependents(ctx, tx, revision); err != nil {
		http.Error(w, "Ошибка зависимых расчётов; откат отменён", 409)
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE bulk_change_operations SET reverted_at=now(),reverted_by=$3 WHERE id=$1 AND revision_id=$2 AND reverted_at IS NULL`, opID, revision, u.ID)
	if err != nil || tag.RowsAffected() != 1 {
		http.Error(w, "Откат не сохранён", 409)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		http.Error(w, "Ошибка фиксации отката", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+revision+"/bulk/history", http.StatusSeeOther)
}

// Normalized comparison is intentionally strict: a subsequent change to any work item
// invalidates the previous undo snapshot, including manual edits and dependent results.

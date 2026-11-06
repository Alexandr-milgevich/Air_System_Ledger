package httpserver

import (
	"context"
	"log/slog"
	"montage-ledger/internal/auth"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The engineer requests a historical-price exception in draft; a different
// authorized reviewer must accept it in in_review. Neither action changes amount.
func (x web) reviewExceptionRequest(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.reviewExceptionChange(w, r, u, false)
}
func (x web) reviewExceptionAccept(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.reviewExceptionChange(w, r, u, true)
}

func (x web) reviewExceptionChange(w http.ResponseWriter, r *http.Request, u auth.Identity, accept bool) {
	if !x.validPost(w, r, u) {
		return
	}
	revision := paramID(w, r)
	if revision == "" {
		return
	}
	item := strings.TrimSpace(r.PostFormValue("item"))
	if !uuidPattern.MatchString(item) {
		http.Error(w, "Некорректная позиция", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if !accept && (utf8.RuneCountInString(reason) < 15 || utf8.RuneCountInString(reason) > 2000) {
		http.Error(w, "Обоснование должно содержать 15–2000 символов", http.StatusBadRequest)
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
	var project, status string
	if err = tx.QueryRow(ctx, `SELECT project_id::text,status FROM project_revisions WHERE id=$1 FOR UPDATE`, revision).Scan(&project, &status); err != nil {
		http.NotFound(w, r)
		return
	}
	permission, err := x.projectPermission(ctx, project, u)
	if err != nil {
		http.Error(w, "Доступ запрещён", 403)
		return
	}
	if (!accept && (status != "draft" || !canEdit(permission))) ||
		(accept && (status != "in_review" || (permission != "approve" && permission != "admin"))) {
		http.Error(w, "Операция недоступна для роли или статуса", 403)
		return
	}
	if !accept {
		// Only historical XLSX snapshots, only actual monetary discrepancies.
		var fingerprint string
		err = tx.QueryRow(ctx, `SELECT review_item_snapshot(i)::text FROM project_work_items i
          WHERE i.id=$1 AND i.revision_id=$2
            AND i.rule_snapshot->>'mode'='legacy_snapshot'
            AND i.rule_snapshot->>'imported_from'='xlsx'
            AND abs(i.amount-round(i.billable_quantity*i.unit_price,2))>0.01`, item, revision).Scan(&fingerprint)
		if err != nil {
			http.Error(w, "Исключение допустимо только для исторической XLSX-строки с расхождением", 409)
			return
		}
		_, err = tx.Exec(ctx, `INSERT INTO review_amount_exceptions
           (revision_id,work_item_id,calculation_snapshot,reason,requested_by)
           VALUES($1,$2,$3::jsonb,$4,$5)
           ON CONFLICT(revision_id,work_item_id) DO UPDATE SET
             calculation_snapshot=EXCLUDED.calculation_snapshot,reason=EXCLUDED.reason,
             requested_by=EXCLUDED.requested_by,requested_at=clock_timestamp(),
             status='pending',accepted_by=NULL,accepted_at=NULL,invalidated_at=NULL`,
			revision, item, fingerprint, reason, u.ID)
	} else {
		// SQL binds approval to the exact current input snapshot. Never trust UI state.
		result, e := tx.Exec(ctx, `UPDATE review_amount_exceptions e SET
          status='accepted',accepted_by=$3,accepted_at=clock_timestamp(),invalidated_at=NULL
          FROM project_work_items i
          WHERE e.revision_id=$1 AND e.work_item_id=$2 AND i.id=e.work_item_id
            AND i.revision_id=e.revision_id AND e.status='pending'
            AND e.requested_by<>$3 AND e.calculation_snapshot=review_item_snapshot(i)
            AND i.rule_snapshot->>'mode'='legacy_snapshot'
            AND i.rule_snapshot->>'imported_from'='xlsx'
            AND abs(i.amount-round(i.billable_quantity*i.unit_price,2))>0.01`, revision, item, u.ID)
		err = e
		if err == nil && result.RowsAffected() != 1 {
			http.Error(w, "Исключение не найдено, устарело или требует другого согласующего", 409)
			return
		}
	}
	if err != nil {
		slog.Error("review amount exception failed", "revision", revision, "error", err)
		http.Error(w, "Не удалось сохранить решение об исключении", 409)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		http.Error(w, "Ошибка сохранения", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+revision+"/review", http.StatusSeeOther)
}

// Calculates all mismatches not covered by an exact accepted snapshot. During
// submission pending requests may pass, but approval requires accepted ones.
func outstandingReviewAmountCount(ctx context.Context, tx pgx.Tx, revision string, approval bool) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM project_work_items i
      LEFT JOIN review_amount_exceptions e ON e.revision_id=i.revision_id
        AND e.work_item_id=i.id AND e.calculation_snapshot=review_item_snapshot(i)
      WHERE i.revision_id=$1 AND
       (i.amount<0 OR i.billable_quantity<0 OR i.unit_price<0 OR
        (abs(i.amount-round(i.billable_quantity*i.unit_price,2))>0.01 AND NOT
          COALESCE((i.rule_snapshot->>'mode'='legacy_snapshot'
           AND i.rule_snapshot->>'imported_from'='xlsx'
           AND e.status=ANY(CASE WHEN $2::boolean THEN ARRAY['accepted']::text[] ELSE ARRAY['pending','accepted']::text[] END)),false)))`, revision, approval).Scan(&count)
	return count, err
}

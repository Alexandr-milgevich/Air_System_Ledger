package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math/big"
	"montage-ledger/internal/auth"
	"montage-ledger/internal/calculation"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

const bulkLimit = 100

type bulkRequest struct {
	IDs                               []string
	Action, Value, LocationID, Digest string
}
type bulkRow struct {
	ID, Name, OldPrice, NewPrice, OldAmount, NewAmount, OldLocation, NewLocation string
}
type bulkView struct {
	RevisionID, CSRF, Action, Value, LocationID, Digest, Before, After string
	Rows                                                               []bulkRow
}

const bulkHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Предпросмотр массовых изменений</title><link rel="stylesheet" href="/assets/app.css"></head><body><main style="max-width:1000px;margin:32px auto;padding:16px"><h1>Предпросмотр массовых изменений</h1><p>Проверьте изменение {{len .Rows}} строк. До подтверждения данные не изменяются.</p><div style="overflow-x:auto"><table style="width:100%"><thead><tr><th>Работа</th><th>Цена до</th><th>Цена после</th><th>Сумма до</th><th>Сумма после</th><th>Место до</th><th>Место после</th></tr></thead><tbody>{{range .Rows}}<tr><td>{{.Name}}</td><td>{{.OldPrice}}</td><td>{{.NewPrice}}</td><td>{{.OldAmount}}</td><td>{{.NewAmount}}</td><td>{{.OldLocation}}</td><td>{{.NewLocation}}</td></tr>{{end}}</tbody></table></div><p><strong>Сумма выбранных строк: {{.Before}} ₽ → {{.After}} ₽</strong></p><p>Если выбранные строки были изменены другим пользователем, сохранение будет отклонено.</p><form method="post" action="/app/revisions/{{.RevisionID}}/bulk/commit"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="action" value="{{.Action}}"><input type="hidden" name="value" value="{{.Value}}"><input type="hidden" name="location_id" value="{{.LocationID}}"><input type="hidden" name="digest" value="{{.Digest}}">{{range .Rows}}<input type="hidden" name="item_ids" value="{{.ID}}">{{end}}<button type="submit">Подтвердить изменения</button></form><p><a href="/app/revisions/{{.RevisionID}}/ledger">Отменить и вернуться к ведомости</a></p></main></body></html>`

var bulkTemplate = template.Must(template.New("bulk").Parse(bulkHTML))

func parseBulkRequest(r *http.Request) (bulkRequest, error) {
	b := bulkRequest{Action: r.PostFormValue("action"), Value: strings.TrimSpace(r.PostFormValue("value")), LocationID: r.PostFormValue("location_id"), Digest: r.PostFormValue("digest")}
	ids := r.PostForm["item_ids"]
	if len(ids) == 0 || len(ids) > bulkLimit {
		return b, errors.New("выберите от 1 до 100 строк")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !uuidPattern.MatchString(id) || seen[id] {
			return b, errors.New("неверные или повторные идентификаторы")
		}
		seen[id] = true
		b.IDs = append(b.IDs, id)
	}
	sort.Strings(b.IDs)
	switch b.Action {
	case "price_fixed":
		if _, e := calculation.Parse(b.Value, 4); e != nil {
			return b, fmt.Errorf("некорректная цена: %w", e)
		}
	case "price_percent":
		v := strings.TrimSpace(strings.ReplaceAll(b.Value, ",", "."))
		if strings.HasPrefix(v, "-") {
			n, e := calculation.Parse(strings.TrimPrefix(v, "-"), 2)
			if e != nil || n.Cmp(big.NewRat(100, 1)) > 0 {
				return b, errors.New("процент от -100 до 1000")
			}
		} else {
			n, e := calculation.Parse(v, 2)
			if e != nil || n.Cmp(big.NewRat(1000, 1)) > 0 {
				return b, errors.New("процент от -100 до 1000")
			}
		}
		b.Value = v
	case "location":
		if b.LocationID != "" && !uuidPattern.MatchString(b.LocationID) {
			return b, errors.New("некорректное место монтажа")
		}
	default:
		return b, errors.New("неизвестная операция")
	}
	return b, nil
}

type bulkStored struct {
	ID, Name, Price, Amount, Billable, LocationID, LocationName string
	ManuallyModified                                            bool
}

func bulkFingerprint(rows []bulkStored) string {
	h := sha256.New()
	for _, row := range rows {
		fmt.Fprintf(h, "%d:%s|%d:%s|%d:%s|%d:%s|%d:%s;", len(row.ID), row.ID, len(row.Price), row.Price, len(row.Amount), row.Amount, len(row.Billable), row.Billable, len(row.LocationID), row.LocationID)
		fmt.Fprintf(h, "modified=%t;", row.ManuallyModified)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func loadBulkRows(ctx context.Context, tx pgx.Tx, revision string, ids []string) ([]bulkStored, error) {
	rows, e := tx.Query(ctx, `SELECT i.id::text,i.name_snapshot,i.unit_price::text,i.amount::text,i.billable_quantity::text,COALESCE(i.location_id::text,''),COALESCE(l.name,''),i.manually_modified FROM project_work_items i LEFT JOIN project_locations l ON l.revision_id=i.revision_id AND l.id=i.location_id WHERE i.revision_id=$1 AND i.id::text=ANY($2::text[]) ORDER BY i.id`, revision, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []bulkStored{}
	for rows.Next() {
		var it bulkStored
		if e = rows.Scan(&it.ID, &it.Name, &it.Price, &it.Amount, &it.Billable, &it.LocationID, &it.LocationName, &it.ManuallyModified); e != nil {
			return nil, e
		}
		result = append(result, it)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(result) != len(ids) {
		return nil, errors.New("выбранные строки больше недоступны")
	}
	return result, nil
}

func bulkChange(b bulkRequest, rows []bulkStored, locationName string) (bulkView, error) {
	out := bulkView{Action: b.Action, Value: b.Value, LocationID: b.LocationID, Rows: make([]bulkRow, 0, len(rows)), Digest: bulkFingerprint(rows)}
	before, after := new(big.Rat), new(big.Rat)
	for _, it := range rows {
		p, e := calculation.Parse(it.Price, 4)
		if e != nil {
			return out, e
		}
		bill, e := calculation.Parse(it.Billable, 8)
		if e != nil {
			return out, e
		}
		oldAmount, e := calculation.Parse(it.Amount, 2)
		if e != nil {
			return out, e
		}
		before.Add(before, oldAmount)
		rr := bulkRow{ID: it.ID, Name: it.Name, OldPrice: it.Price, OldAmount: it.Amount, OldLocation: it.LocationName, NewLocation: it.LocationName}
		switch b.Action {
		case "price_fixed":
			p, e = calculation.Parse(b.Value, 4)
			if e != nil {
				return out, e
			}
		case "price_percent":
			sign := int64(1)
			value := b.Value
			if strings.HasPrefix(value, "-") {
				sign = -1
				value = strings.TrimPrefix(value, "-")
			}
			percent, err := calculation.Parse(value, 2)
			if err != nil {
				return out, err
			}
			percent.Mul(percent, big.NewRat(sign, 1))
			ratio := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(percent, big.NewRat(100, 1)))
			p.Mul(p, ratio)
			p, _ = new(big.Rat).SetString(calculation.Fixed(p, 4))
		case "location":
			rr.NewLocation = locationName
		}
		if p.Sign() < 0 || p.Cmp(new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil))) >= 0 {
			return out, errors.New("цена выходит за пределы NUMERIC(20,4)")
		}
		rr.NewPrice = calculation.Fixed(p, 4)
		// Use the same stored billable_quantity as the single-row editor.
		if b.Action == "location" {
			rr.NewAmount = it.Amount
		} else {
			rr.NewAmount = calculation.Fixed(new(big.Rat).Mul(bill, p), 2)
		}
		amount, _ := calculation.Parse(rr.NewAmount, 2)
		after.Add(after, amount)
		out.Rows = append(out.Rows, rr)
	}
	out.Before = calculation.Fixed(before, 2)
	out.After = calculation.Fixed(after, 2)
	return out, nil
}

func (x web) bulkProcess(w http.ResponseWriter, r *http.Request, u auth.Identity, commit bool) {
	revision, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	b, e := parseBulkRequest(r)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	tx, e := x.db.Begin(ctx)
	if e != nil {
		http.Error(w, "Ошибка транзакции", 500)
		return
	}
	defer tx.Rollback(ctx)
	if _, auditErr := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, u.ID); auditErr != nil {
		http.Error(w, "Ошибка контекста аудита", 500)
		return
	}
	var status string
	if e = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 FOR UPDATE`, revision).Scan(&status); e != nil || status != "draft" {
		http.Error(w, "Ревизия закрыта", 409)
		return
	}
	locationName := ""
	if b.Action == "location" && b.LocationID != "" {
		if e = tx.QueryRow(ctx, `SELECT name FROM project_locations WHERE revision_id=$1 AND id=$2`, revision, b.LocationID).Scan(&locationName); e != nil {
			http.Error(w, "Место монтажа не найдено", 400)
			return
		}
	}
	rows, e := loadBulkRows(ctx, tx, revision, b.IDs)
	if e != nil {
		http.Error(w, "Не удалось прочитать выбранные строки: "+e.Error(), 409)
		return
	}
	view, e := bulkChange(b, rows, locationName)
	if e != nil {
		http.Error(w, "Ошибка расчёта: "+e.Error(), 400)
		return
	}
	if !commit {
		view.RevisionID = revision
		view.CSRF = x.csrf(r, u)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		if e = bulkTemplate.Execute(w, view); e != nil {
			slog.Error("bulk preview template failed", "error", e)
		}
		return
	}
	if len(b.Digest) != 64 || subtle.ConstantTimeCompare([]byte(b.Digest), []byte(view.Digest)) != 1 {
		http.Error(w, "Строки изменились после предпросмотра. Повторите операцию.", 409)
		return
	}
	for _, it := range view.Rows {
		var affected int64
		if b.Action == "location" {
			var loc any
			if b.LocationID != "" {
				loc = b.LocationID
			}
			tag, err := tx.Exec(ctx, `UPDATE project_work_items SET location_id=$3,manually_modified=true WHERE revision_id=$1 AND id=$2`, revision, it.ID, loc)
			e = err
			affected = tag.RowsAffected()
		} else {
			tag, err := tx.Exec(ctx, `UPDATE project_work_items SET unit_price=$3,amount=$4,manually_modified=true WHERE revision_id=$1 AND id=$2`, revision, it.ID, it.NewPrice, it.NewAmount)
			e = err
			affected = tag.RowsAffected()
		}
		if e != nil || affected != 1 {
			slog.Error("bulk update failed", "error", e, "revision", revision)
			http.Error(w, "Ошибка массового изменения; изменения отменены", 409)
			return
		}
	}
	if e = refreshDependents(ctx, tx, revision); e != nil {
		http.Error(w, "Ошибка зависимых расчётов; изменения отменены", 409)
		return
	}
	// Obtain actual final values after dependency recalculation. A dependent work
	// item may itself be selected, so preview estimates need not be final.
	finalRows, finalErr := loadBulkRows(ctx, tx, revision, b.IDs)
	if finalErr != nil {
		http.Error(w, "Ошибка чтения пересчитанных строк", 500)
		return
	}
	finalMap := make(map[string]bulkStored, len(finalRows))
	actualTotal := new(big.Rat)
	for _, row := range finalRows {
		finalMap[row.ID] = row
		amount, parseErr := calculation.Parse(row.Amount, 2)
		if parseErr != nil {
			http.Error(w, "Ошибка результата пересчёта", 500)
			return
		}
		actualTotal.Add(actualTotal, amount)
	}
	view.After = calculation.Fixed(actualTotal, 2)
	// Persist the complete change set in the SAME transaction as the work-item updates.
	// This hash is calculated after recalculating all dependent items.
	postHash, hashErr := revisionFingerprint(ctx, tx, revision)
	if hashErr != nil {
		http.Error(w, "Ошибка снимка ревизии; изменения отменены", 500)
		return
	}
	var operationID string
	e = tx.QueryRow(ctx, `INSERT INTO bulk_change_operations(revision_id,actor_id,action,value,before_total,after_total,post_revision_hash) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, revision, u.ID, b.Action, b.Value, view.Before, view.After, postHash).Scan(&operationID)
	if e != nil {
		slog.Error("bulk audit insert failed", "error", e)
		http.Error(w, "Не удалось записать журнал; изменения отменены", 500)
		return
	}
	previous := make(map[string]bulkStored, len(rows))
	for _, row := range rows {
		previous[row.ID] = row
	}
	for _, row := range view.Rows {
		old := previous[row.ID]
		final := finalMap[row.ID]
		var oldLoc, newLoc any
		if old.LocationID != "" {
			oldLoc = old.LocationID
		}
		newLocation := old.LocationID
		if b.Action == "location" {
			newLocation = b.LocationID
		}
		if newLocation != "" {
			newLoc = newLocation
		}
		_, e = tx.Exec(ctx, `INSERT INTO bulk_change_items(operation_id,item_id,old_price,new_price,old_amount,new_amount,old_location_id,new_location_id,old_manually_modified,new_manually_modified) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, operationID, row.ID, old.Price, final.Price, old.Amount, final.Amount, oldLoc, newLoc, old.ManuallyModified, final.ManuallyModified)
		if e != nil {
			slog.Error("bulk audit item failed", "error", e)
			http.Error(w, "Не удалось записать журнал позиций; изменения отменены", 500)
			return
		}
	}
	if e = tx.Commit(ctx); e != nil {
		http.Error(w, "Ошибка фиксации; изменения отменены", 500)
		return
	}
	http.Redirect(w, r, "/app/revisions/"+revision+"/ledger", http.StatusSeeOther)
}
func (x web) bulkPreview(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.bulkProcess(w, r, u, false)
}
func (x web) bulkCommit(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	x.bulkProcess(w, r, u, true)
}

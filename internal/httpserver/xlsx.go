package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"montage-ledger/internal/auth"
	"montage-ledger/internal/xlsxledger"
)

// Pending imports are intentionally short-lived, process-local and tied to a user and revision.
// Restarting the server discards previews; no untrusted XLSX is stored as a public file.
type pendingXLSX struct {
	UserID, RevisionID string
	Data               []byte
	Created            time.Time
}

var pendingImports = struct {
	sync.Mutex
	Files map[string]pendingXLSX
}{Files: map[string]pendingXLSX{}}

func pendingPut(user, revision string, data []byte) (string, error) {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	pendingImports.Lock()
	defer pendingImports.Unlock()
	for key, v := range pendingImports.Files {
		if time.Since(v.Created) > 20*time.Minute {
			delete(pendingImports.Files, key)
		}
	}
	if len(pendingImports.Files) >= 32 {
		return "", fmt.Errorf("очередь импорта заполнена; повторите попытку позже")
	}
	pendingImports.Files[token] = pendingXLSX{UserID: user, RevisionID: revision, Data: append([]byte(nil), data...), Created: time.Now()}
	return token, nil
}
func pendingGet(token, user, revision string) ([]byte, bool) {
	pendingImports.Lock()
	defer pendingImports.Unlock()
	v, ok := pendingImports.Files[token]
	if !ok || v.UserID != user || v.RevisionID != revision || time.Since(v.Created) > 20*time.Minute {
		return nil, false
	}
	return append([]byte(nil), v.Data...), true
}
func pendingDelete(token string) {
	pendingImports.Lock()
	delete(pendingImports.Files, token)
	pendingImports.Unlock()
}

const xlsxHTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Excel · Монтажные ведомости</title><style>body{font:15px/1.5 system-ui;background:#f4f6fa;color:#172437;margin:0}main{max-width:1200px;margin:30px auto;padding:0 16px}.card{background:white;border:1px solid #d8e0eb;padding:20px;border-radius:10px;margin:16px 0}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #ddd;padding:7px;text-align:left}th{background:#e8edf6}.scroll{max-height:500px;overflow:auto}button{background:#245ab0;color:white;border:0;border-radius:5px;padding:10px 17px;cursor:pointer}.warn{color:#9f4b00}a{color:#245ab0}</style><main><a href="/app/revisions/{{.RevisionID}}/ledger">← Вернуться к ведомости</a><h1>Импорт XLSX · предварительный просмотр</h1><div class="card"><p>Импорт разрешён только для <b>пустой черновой ревизии</b>. Все  строки проверяются до записи. Данные сохраняются одним SQL-коммитом.</p><form action="/app/revisions/{{.RevisionID}}/xlsx/preview" method="post" enctype="multipart/form-data"><input name="csrf" type="hidden" value="{{.CSRF}}"><input type="file" name="file" required accept=".xlsx"><button>Проверить файл</button></form></div>{{if .Error}}<div class="card warn">{{.Error}}</div>{{end}}{{if .Preview}}<div class="card"><h2>Результат: {{len .Preview.Items}} строк, сумма {{.Preview.Total}} ₽</h2><p>Лист: {{.Preview.Sheet}}. Предупреждений: {{len .Preview.Warnings}}.</p>{{range .Preview.Warnings}}<p class="warn">{{.}}</p>{{end}}<div class="scroll"><table><thead><tr><th>Excel</th><th>Система</th><th>Наименование</th><th>Кол.</th><th>Ед.</th><th>Коэф.</th><th>Цена</th><th>Итого</th></tr></thead><tbody>{{range .Preview.Items}}<tr><td>{{.Row}}</td><td>{{.SystemCode}}</td><td>{{.Name}}</td><td>{{.Quantity}}</td><td>{{.Unit}}</td><td>{{.Coefficient}}</td><td>{{.Price}}</td><td>{{.Amount}}</td></tr>{{end}}</tbody></table></div><form action="/app/revisions/{{.RevisionID}}/xlsx/commit" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="token" value="{{.Token}}"><button>Подтвердить импорт {{len .Preview.Items}} строк</button></form></div>{{end}}</main>`

var xlsxTemplate = template.Must(template.New("xlsx").Parse(xlsxHTML))

type xlsxView struct {
	RevisionID, CSRF, Error, Token string
	Preview                        *xlsxledger.Preview
}

func xlsxRender(w http.ResponseWriter, status int, v xlsxView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = xlsxTemplate.Execute(w, v)
}
func (x web) xlsxAccess(w http.ResponseWriter, r *http.Request, u auth.Identity, write bool) (string, bool) {
	id := paramID(w, r)
	if id == "" {
		return "", false
	}
	ctx, cancel := shortContext(r)
	defer cancel()
	projectID, status, _, err := x.revisionAccess(ctx, id, u)
	if err != nil {
		http.Error(w, "Ревизия не найдена", 404)
		return "", false
	}
	if write {
		permission, err := x.projectPermission(ctx, projectID, u)
		if err != nil || !canEdit(permission) {
			http.Error(w, "Нет прав на изменение", 403)
			return "", false
		}
		if status != "draft" {
			http.Error(w, "Импорт возможен только в черновик", 409)
			return "", false
		}
	}
	return id, true
}
func xlsxCSRF(r *http.Request, csrf string) bool {
	return sameOrigin(r) && csrf != "" && r.PostFormValue("csrf") == csrf
}
func (x web) xlsxGet(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.xlsxAccess(w, r, u, true)
	if !ok {
		return
	}
	xlsxRender(w, 200, xlsxView{RevisionID: id, CSRF: x.csrf(r, u)})
}
func (x web) xlsxPreview(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.xlsxAccess(w, r, u, true)
	if !ok {
		return
	}
	csrf := x.csrf(r, u)
	if !sameOrigin(r) {
		http.Error(w, "Cross-site request denied", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, xlsxledger.MaxUpload+(128<<10))
	if err := r.ParseMultipartForm(xlsxledger.MaxUpload); err != nil {
		http.Error(w, "Слишком большой или неправильный запрос", 400)
		return
	}
	if !xlsxCSRF(r, csrf) {
		http.Error(w, "CSRF validation failed", 403)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Не выбран XLSX", 400)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, xlsxledger.MaxUpload+1))
	if err != nil || len(data) > xlsxledger.MaxUpload {
		http.Error(w, "Максимальный файл — 2 МБ", 400)
		return
	}
	p, err := xlsxledger.Parse(data)
	if err != nil {
		xlsxRender(w, 422, xlsxView{RevisionID: id, CSRF: csrf, Error: err.Error()})
		return
	}
	// Preview is not a reservation: commit re-checks that the revision is still empty.
	token, err := pendingPut(u.ID, id, data)
	if err != nil {
		http.Error(w, "Импорт временно недоступен", 503)
		return
	}
	xlsxRender(w, 200, xlsxView{RevisionID: id, CSRF: csrf, Token: token, Preview: &p})
}
func (x web) xlsxCommit(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.revisionWrite(w, r, u)
	if !ok {
		return
	}
	token := r.PostFormValue("token")
	data, ok := pendingGet(token, u.ID, id)
	if !ok {
		http.Error(w, "Предпросмотр истёк — загрузите XLSX заново", 409)
		return
	}
	p, err := xlsxledger.Parse(data)
	if err != nil {
		http.Error(w, "Файл не прошёл повторную проверку", 422)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tx, err := x.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Ошибка базы", 500)
		return
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM project_revisions WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil || status != "draft" {
		http.Error(w, "Ревизия закрыта", 409)
		return
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM project_work_items WHERE revision_id=$1`, id).Scan(&count); err != nil || count != 0 {
		http.Error(w, "Для импорта нужна пустая ведомость; существующие строки не изменены", 409)
		return
	}
	systems := map[string]string{}
	locations := map[string]string{}
	for _, it := range p.Items {
		sid, found := systems[it.SystemCode]
		if !found {
			typ := "exhaust"
			if strings.HasPrefix(it.SystemCode, "П-") {
				typ = "supply"
			}
			err = tx.QueryRow(ctx, `INSERT INTO project_systems(revision_id,code,name,type_code,sort_order) VALUES($1,$2,$3,$4,(SELECT COALESCE(MAX(sort_order),0)+1 FROM project_systems WHERE revision_id=$1)) ON CONFLICT(revision_id,code) DO UPDATE SET code=EXCLUDED.code RETURNING id::text`, id, it.SystemCode, it.SystemName, typ).Scan(&sid)
			if err != nil {
				break
			}
			systems[it.SystemCode] = sid
		}
		var loc any
		if name := strings.TrimSpace(it.Location); name != "" {
			lid, found := locations[strings.ToLower(name)]
			if !found {
				err = tx.QueryRow(ctx, `INSERT INTO project_locations(revision_id,name) VALUES($1,$2) ON CONFLICT(revision_id,name) DO UPDATE SET name=EXCLUDED.name RETURNING id::text`, id, name).Scan(&lid)
				if err != nil {
					break
				}
				locations[strings.ToLower(name)] = lid
			}
			loc = lid
		}
		snap, _ := json.Marshal(map[string]any{"mode": it.Mode, "rule_version": 3, "imported_from": "xlsx", "excel_row": it.Row})
		trace, _ := json.Marshal(map[string]any{"source": "xlsx", "excel_row": it.Row, "warning": it.Warning, "amount_snapshot": it.Amount})
		_, err = tx.Exec(ctx, `INSERT INTO project_work_items(revision_id,system_id,location_id,name_snapshot,source_type,input_quantity,input_unit_code,coefficient,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount,rule_snapshot,inputs_snapshot,trace_snapshot,sort_order) VALUES($1,$2,$3,$4,'manual',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,'{}'::jsonb,$15::jsonb,$16)`, id, sid, loc, it.Name, it.Quantity, it.Unit, nullableCoefficient(it.Coefficient), it.Calculated, it.ResultUnit, it.Billable, it.BillableUnit, it.Price, it.Amount, string(snap), string(trace), it.Row)
		if err != nil {
			break
		}
	}
	if err != nil {
		slog.Error("xlsx import failed", "error", err, "revision", id)
		http.Error(w, "Импорт отменён; база не изменилась", 500)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		slog.Error("xlsx commit failed", "error", err)
		http.Error(w, "Ошибка сохранения", 500)
		return
	}
	pendingDelete(token)
	http.Redirect(w, r, "/app/revisions/"+id+"/ledger", http.StatusSeeOther)
}
func (x web) xlsxExport(w http.ResponseWriter, r *http.Request, u auth.Identity) {
	id, ok := x.xlsxAccess(w, r, u, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := x.db.Query(ctx, `SELECT s.code,s.name,i.name_snapshot,i.input_unit_code,i.input_quantity::text,COALESCE(i.coefficient::text,''),i.calculated_quantity::text,i.unit_price::text,i.amount::text,COALESCE(l.name,'') FROM project_work_items i JOIN project_systems s ON s.id=i.system_id AND s.revision_id=i.revision_id LEFT JOIN project_locations l ON l.id=i.location_id AND l.revision_id=i.revision_id WHERE i.revision_id=$1 ORDER BY s.sort_order,s.code,i.sort_order,i.id`, id)
	if err != nil {
		http.Error(w, "Ошибка чтения ведомости", 500)
		return
	}
	items := []xlsxledger.Item{}
	for rows.Next() {
		var it xlsxledger.Item
		err = rows.Scan(&it.SystemCode, &it.SystemName, &it.Name, &it.Unit, &it.Quantity, &it.Coefficient, &it.Calculated, &it.Price, &it.Amount, &it.Location)
		if err != nil {
			break
		}
		items = append(items, it)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "Ошибка чтения ведомости", 500)
		return
	}
	var buf bytes.Buffer
	if err = xlsxledger.Export(&buf, items); err != nil {
		slog.Error("xlsx export failed", "error", err)
		http.Error(w, "Ошибка Excel", 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ledger-%s.xlsx"`, id))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(buf.Bytes())
}

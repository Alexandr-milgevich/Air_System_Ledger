package httpserver

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestStage130ProgressTemplateEscapesNamesAndNotes(t *testing.T) {
	d := progressPage{RevisionID: "11111111-1111-1111-1111-111111111111", ProjectName: "<script>alert(1)</script>", TotalPending: "1", CanApprove: true,
		Systems: []progressSystem{{Code: "В-42", Name: "Тест", Items: "2", Complete: "1", RecordedPercent: "50.0", ApprovedPercent: "25.0", PendingEvents: "1"}},
		Pending: []progressPending{{ID: "22222222-2222-2222-2222-222222222222", Worker: "Монтажник", System: "В-42", Work: "<img src=x onerror=alert(1)>", Quantity: "1.00", Unit: "pcs", Date: "2026-11-07", Note: "<script>evil()</script>"}},
	}
	var b bytes.Buffer
	if err := progressTemplate.Execute(&b, d); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Contains(s, "<script>") || strings.Contains(s, "<img src=x") {
		t.Fatal("unescaped HTML")
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;img", `value="50.0"`, `value="25.0"`, `/execution/22222222-2222-2222-2222-222222222222/approve`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestStage130ProgressSQLAndAggregation(t *testing.T) {
	db := stage128Pool(t)
	user, _, revision, system, item, _, assignment := fixture114(t, db)
	ctx := context.Background()
	// A second item with a different physical unit must not be added to the first one's quantity.
	secondSystem := httpTestID(t, db, `INSERT INTO project_systems(revision_id,code,name,type_code) VALUES($1,'В-99','Вторая система','exhaust') RETURNING id::text`, revision)
	_ = system
	_ = item
	httpTestExec(t, db, `INSERT INTO project_work_items(revision_id,system_id,name_snapshot,input_quantity,input_unit_code,calculated_quantity,result_unit_code,billable_quantity,billable_unit_code,unit_price,amount) VALUES($1,$2,'Второй тип работы',3,'pcs',3,'pcs',3,'pcs',150,450)`, revision, secondSystem)
	// fixture assignment has planned 6, approved 0. Record 3 and approve it.
	event := httpTestID(t, db, `INSERT INTO work_execution_events(assignment_id,event_type,quantity_delta,performed_on,created_by) VALUES($1,'performed',3,current_date,$2) RETURNING id::text`, assignment, user)
	x := web{db: db}
	d := progressPage{RevisionID: revision}
	if err := x.loadExecutionProgress(ctx, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Systems) != 2 {
		t.Fatalf("got %d systems", len(d.Systems))
	}
	if d.TotalPending != "1" || len(d.Pending) != 1 {
		t.Fatalf("pending=%s rows=%d", d.TotalPending, len(d.Pending))
	}
	// Systems are sorted by sort_order and code, not insertion order.
	// Look up the fixture system by its stable code rather than assuming index 0.
	var supply *progressSystem
	for i := range d.Systems {
		if d.Systems[i].Code == "П-31" {
			supply = &d.Systems[i]
			break
		}
	}
	if supply == nil {
		t.Fatalf("П-31 not found in systems: %+v", d.Systems)
	}
	if mustRat(supply.RecordedPercent).Cmp(mustRat("50")) != 0 || mustRat(supply.ApprovedPercent).Sign() != 0 {
		t.Fatalf("unexpected П-31 progress %+v", *supply)
	}
	httpTestExec(t, db, `INSERT INTO work_execution_approvals(event_id,approved_by) VALUES($1,$2)`, event, user)
	d = progressPage{RevisionID: revision}
	if err := x.loadExecutionProgress(ctx, &d); err != nil {
		t.Fatal(err)
	}
	if d.TotalPending != "0" || len(d.Pending) != 0 {
		t.Fatalf("approval queue still populated")
	}
	supply = nil
	for i := range d.Systems {
		if d.Systems[i].Code == "П-31" {
			supply = &d.Systems[i]
			break
		}
	}
	if supply == nil || mustRat(supply.ApprovedPercent).Cmp(mustRat("50")) != 0 {
		t.Fatalf("unexpected П-31 approved progress; systems=%+v", d.Systems)
	}
	if len(d.Workers) != 1 || d.Workers[0].Accrued != "600.00" {
		t.Fatalf("worker totals %+v", d.Workers)
	}
}

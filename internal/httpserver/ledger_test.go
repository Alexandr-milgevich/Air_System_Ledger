package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLedgerTemplate(t *testing.T) {
	for _, writable := range []bool{false, true} {
		rr := httptest.NewRecorder()
		ledgerRender(rr, 200, ledgerView{RevisionID: "11111111-1111-1111-1111-111111111111", ProjectID: "22222222-2222-2222-2222-222222222222", ProjectName: "Объект", CanWrite: writable, Total: "4800.00", VisibleCount: 1, TotalCount: 1, CSRF: "csrf-token", Systems: []ledgerSystem{{ID: "33333333-3333-3333-3333-333333333333", Code: "П-31", Name: "Приточная", Total: "4800.00", Visible: true, Items: []ledgerItem{{ID: "44444444-4444-4444-4444-444444444444", Name: "Воздуховод", Quantity: "5", InputUnit: "m", Calculated: "6", ResultUnit: "m2", Amount: "4800.00", Mode: "coefficient"}}}}})
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Воздуховод") || !strings.Contains(rr.Body.String(), "4800.00") {
			t.Fatalf("template output unexpected: %s", rr.Body.String())
		}
		if writable && !strings.Contains(rr.Body.String(), "csrf-token") {
			t.Fatal("csrf missing")
		}
		if !writable && strings.Contains(rr.Body.String(), "Удалить") {
			t.Fatal("read only has delete control")
		}
	}
}
func TestValidateItem(t *testing.T) {
	input := itemInput{Name: "Воздуховод", Quantity: "5", Unit: "m", Mode: "coefficient", Coefficient: "1.2", Price: "800"}
	result, unit, e := validateItem(input)
	if e != nil || unit != "m2" || result.Amount != "4800.00" {
		t.Fatalf("unexpected result %+v, %s, %v", result, unit, e)
	}
	input.Unit = "m2"
	if _, _, e = validateItem(input); e == nil {
		t.Fatal("m2 coefficient accepted")
	}
	input.Unit = "m"
	input.LocationID = "../"
	if _, _, e = validateItem(input); e == nil {
		t.Fatal("invalid location accepted")
	}
}

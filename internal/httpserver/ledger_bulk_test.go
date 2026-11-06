package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

const idOne = "11111111-1111-1111-1111-111111111111"
const idTwo = "22222222-2222-2222-2222-222222222222"

func bulkInput(body string) (bulkRequest, error) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	return parseBulkRequest(r)
}
func TestBulkParse(t *testing.T) {
	cases := []struct {
		body  string
		valid bool
	}{
		{"action=price_percent&value=10&item_ids=" + idOne, true},
		{"action=price_percent&value=-100&item_ids=" + idOne, true},
		{"action=price_percent&value=-100.01&item_ids=" + idOne, false},
		{"action=price_percent&value=1000.01&item_ids=" + idOne, false},
		{"action=price_fixed&value=350.1250&item_ids=" + idOne, true},
		{"action=price_fixed&value=-1&item_ids=" + idOne, false},
		{"action=location&item_ids=" + idOne + "&item_ids=" + idOne, false},
		{"action=location&item_ids=bad", false},
		{"action=unknown&item_ids=" + idOne, false},
	}
	for _, tc := range cases {
		_, err := bulkInput(tc.body)
		if (err == nil) != tc.valid {
			t.Errorf("body %s: err %v", tc.body, err)
		}
	}
}
func TestBulkPriceAndExactAmounts(t *testing.T) {
	rows := []bulkStored{{ID: idOne, Name: "Воздуховод", Price: "800.0000", Amount: "4800.00", Billable: "6.00000000"}, {ID: idTwo, Name: "Клапан", Price: "350.0000", Amount: "1050.00", Billable: "3.00000000"}}
	req := bulkRequest{Action: "price_percent", Value: "10"}
	out, err := bulkChange(req, rows, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Before != "5850.00" || out.After != "6435.00" || out.Rows[0].NewPrice != "880.0000" || out.Rows[0].NewAmount != "5280.00" {
		t.Fatalf("unexpected bulk result: %+v", out)
	}
	req = bulkRequest{Action: "price_fixed", Value: "200"}
	out, err = bulkChange(req, rows, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.After != "1800.00" {
		t.Fatalf("fixed price total=%s", out.After)
	}
	req = bulkRequest{Action: "location", LocationID: idOne}
	out, err = bulkChange(req, rows, "Коридор")
	if err != nil {
		t.Fatal(err)
	}
	if out.After != out.Before || out.Rows[0].NewLocation != "Коридор" {
		t.Fatalf("location: %+v", out)
	}
}
func TestBulkSnapshotDigestChanges(t *testing.T) {
	a := []bulkStored{{ID: idOne, Price: "100.0000", Amount: "100.00", Billable: "1.00000000"}}
	before := bulkFingerprint(a)
	a[0].Price = "101.0000"
	if before == bulkFingerprint(a) {
		t.Fatal("price edit must invalidate preview")
	}
	a[0].Price = "100.0000"
	a[0].Billable = "2.00000000"
	if before == bulkFingerprint(a) {
		t.Fatal("quantity edit must invalidate preview")
	}
	a[0].Billable = "1.00000000"
	a[0].LocationID = idTwo
	if before == bulkFingerprint(a) {
		t.Fatal("location edit must invalidate preview")
	}
}

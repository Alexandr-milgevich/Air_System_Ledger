package calculation

import "testing"

func TestCompute(t *testing.T) {
	for _, tc := range []struct{ mode, q, c, p, calculated, amount string }{
		{"coefficient", "5", "1.2", "800", "6.00000000", "4800.00"},
		{"coefficient", "10", "0.63", "800", "6.30000000", "5040.00"},
		{"direct", "3", "", "350", "3.00000000", "1050.00"},
		{"direct", "0.33333333", "", "1", "0.33333333", "0.33"},
		{"direct", "1", "", "0.005", "1.00000000", "0.01"},
	} {
		got, err := Compute(tc.mode, tc.q, tc.c, tc.p)
		if err != nil {
			t.Fatal(err)
		}
		if got.Calculated != tc.calculated || got.Amount != tc.amount {
			t.Fatalf("%+v got %+v", tc, got)
		}
	}
}
func TestInvalid(t *testing.T) {
	for _, tc := range []struct{ m, q, c, p string }{
		{"direct", "-1", "", "10"}, {"direct", "1e2", "", "10"}, {"direct", "1.123456789", "", "10"},
		{"coefficient", "2", "-1", "10"}, {"coefficient", "1", "1", "-10"},
		{"unknown", "1", "", "1"},
	} {
		if _, err := Compute(tc.m, tc.q, tc.c, tc.p); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
func TestDecimalComma(t *testing.T) {
	g, e := Compute("coefficient", "2,5", "0,4", "100")
	if e != nil || g.Amount != "100.00" {
		t.Fatalf("got %v %v", g, e)
	}
}

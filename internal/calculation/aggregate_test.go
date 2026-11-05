package calculation

import "testing"

func TestAggregateArea(t *testing.T) {
	r, e := AggregateArea([]string{"6.00000000", "3.00000000"}, []string{"1.00000000"}, "1.5", "200")
	if e != nil || r.Calculated != "12.00000000" || r.Amount != "2400.00" {
		t.Fatalf("result=%+v err=%v", r, e)
	}
	_, e = AggregateArea([]string{"1"}, []string{"2"}, "1", "100")
	if e == nil {
		t.Fatal("accepted negative area")
	}
	_, e = AggregateArea(nil, nil, "1", "100")
	if e == nil {
		t.Fatal("accepted empty sources")
	}
}

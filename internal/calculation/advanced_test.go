package calculation

import "testing"

func TestGeometry(t *testing.T) {
	cases := []struct{ mode, l, a, b, p, area, amount string }{
		{"geometry_rectangular", "5", "400", "200", "800", "6.00000000", "4800.00"},
		{"geometry_round", "10", "200", "", "800", "6.28318531", "5026.55"},
	}
	for _, tt := range cases {
		t.Run(tt.mode, func(t *testing.T) {
			r, e := Geometry(tt.mode, tt.l, tt.a, tt.b, tt.p)
			if e != nil {
				t.Fatal(e)
			}
			if r.Calculated != tt.area || r.Amount != tt.amount {
				t.Fatalf("got %+v", r)
			}
		})
	}
}
func TestLinked(t *testing.T) {
	r, e := Linked("6.00000000", "1.5", "200")
	if e != nil || r.Amount != "1800.00" {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestInvalidDimensions(t *testing.T) {
	for _, tc := range []struct{ mode, a, b string }{{"geometry_round", "0", ""}, {"geometry_rectangular", "400", "0"}, {"geometry_round", "-10", ""}} {
		if _, e := Geometry(tc.mode, "5", tc.a, tc.b, "800"); e == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

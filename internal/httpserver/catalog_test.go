package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStage14CatalogInput(t *testing.T) {
	tests := []struct {
		name, unit, mode, qty, coeff, price, width, height, diam string
		valid                                                    bool
	}{
		{"Отвод", "pcs", "reference_coefficient", "1", "0.116", "800", "", "", "", true},
		{"Канал", "m", "geometry_rectangular", "1", "1", "800", "400", "200", "", true},
		{"Круглый канал", "m", "geometry_round", "1", "1", "800", "", "", "200", true},
		{"Позиция", "m2", "direct", "1", "1", "100", "", "", "", true},
		{"Ошибка", "m2", "coefficient", "1", "1", "100", "", "", "", false},
		{"Ошибка", "pcs", "sum_items", "1", "1", "100", "", "", "", false},
		{"", "pcs", "direct", "1", "1", "100", "", "", "", false},
		{"Ошибка", "pcs", "direct", "1", "1", "-100", "", "", "", false},
	}
	for _, tc := range tests {
		_, _, err := catalogInput(tc.name, "Монтаж", tc.unit, tc.mode, tc.qty, tc.coeff, tc.price, tc.width, tc.height, tc.diam)
		if (err == nil) != tc.valid {
			t.Errorf("%+v: err=%v", tc, err)
		}
	}
}
func TestStage14CatalogTemplateEscapesInput(t *testing.T) {
	rr := httptest.NewRecorder()
	d := catalogPage{CSRF: "token", Admin: true, Rows: []catalogRow{{ID: "11111111-1111-1111-1111-111111111111", Name: `<script>alert(1)</script>`, Category: `<img src=x onerror=1>`, Price: "800", Active: true}}}
	if err := catalogTemplate.Execute(rr, d); err != nil {
		t.Fatal(err)
	}
	html := rr.Body.String()
	if strings.Contains(html, "<script>alert(1)</script>") || strings.Contains(html, "<img src=x onerror=1>") {
		t.Fatal("template failed to escape catalog data")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("expected escaped name")
	}
}

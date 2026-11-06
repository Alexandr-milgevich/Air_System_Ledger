package xlsxledger

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceWorkbook(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "legacy_reference.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 124 {
		t.Fatalf("got %d items want 124", len(preview.Items))
	}
	if preview.Total != "182607.09" {
		t.Fatalf("total=%s", preview.Total)
	}
	systems := map[string]bool{}
	for _, it := range preview.Items {
		systems[it.SystemCode] = true
	}
	if len(systems) != 9 {
		t.Fatalf("systems=%v", systems)
	}
	if len(preview.Warnings) < 1 {
		t.Fatal("expected legacy anomaly warning")
	}
	var out bytes.Buffer
	if err = Export(&out, preview.Items); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("PK")) {
		t.Fatal("invalid xlsx output")
	}
}
func TestRejectInvalid(t *testing.T) {
	for _, v := range [][]byte{nil, []byte("not XLSX"), make([]byte, MaxUpload+1)} {
		if _, err := Parse(v); err == nil {
			t.Fatal("accepted invalid XLSX")
		}
	}
}
func TestInjection(t *testing.T) {
	for _, s := range []string{"=HYPERLINK(\"bad\")", "+1", "-2", "@foo"} {
		if !strings.HasPrefix(safeText(s), "'") {
			t.Fatalf("unsafe %q", s)
		}
	}
}

// Excel serializes some ordinary decimals with long floating-point tails.
// Regression: row E10 in the supplied reference workbook.
func TestExcelDecimalPrecision(t *testing.T) {
	cases := []struct {
		raw   string
		scale int
		want  string
	}{
		{"0.28899999999999998", 10, "0.2890000000"},
		{"0.22700000000000001", 10, "0.2270000000"},
		{"1.2000000000000002", 10, "1.2000000000"},
		{"6.283185307179586", 8, "6.28318531"},
		{"2.89E-1", 10, "0.2890000000"},
	}
	for _, tc := range cases {
		got, err := numeric(tc.raw, tc.scale)
		if err != nil || got != tc.want {
			t.Errorf("numeric(%q, %d) = %q, %v; want %q", tc.raw, tc.scale, got, err, tc.want)
		}
	}
	for _, raw := range []string{"abc", "-0.2", "1+2", "1;2", "999999999999999999999999"} {
		if _, err := numeric(raw, 10); err == nil {
			t.Errorf("accepted malformed/out-of-range numeric %q", raw)
		}
	}
}

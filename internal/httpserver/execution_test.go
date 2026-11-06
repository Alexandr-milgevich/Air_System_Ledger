package httpserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestSafeExcelText(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"=1+2", "'=1+2"}, {"+SUM(A1)", "'+SUM(A1)"},
		{"-2", "'-2"}, {"@A1", "'@A1"}, {"\t=1", "'\t=1"},
		{"Монтаж воздуховода", "Монтаж воздуховода"},
	} {
		if got := safeExcelText(tc.input); got != tc.want {
			t.Errorf("safeExcelText(%q)=%q want=%q", tc.input, got, tc.want)
		}
	}
}

func TestExecutionTemplate(t *testing.T) {
	var out bytes.Buffer
	err := executionTemplate.Execute(&out, executionData{
		RevisionID: "00000000-0000-0000-0000-000000000000", ProjectName: "Тест <проект>", Status: "draft", TotalApproved: "11200.00",
		Items: []executionItem{{Name: "Воздуховод", Quantity: "50", Recorded: "32", Unit: "m2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "11200.00") || strings.Contains(text, "<проект>") {
		t.Fatal("HTML template output missing amount or failed escaping")
	}
}

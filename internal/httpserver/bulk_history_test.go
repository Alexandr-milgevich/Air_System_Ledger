package httpserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestBulkHistoryTemplate(t *testing.T) {
	data := bulkHistoryView{
		RevisionID: idOne, CSRF: "test-token", CanWrite: true,
		Records: []bulkHistoryRecord{{ID: idTwo, Action: "price_percent", Value: "10", Actor: "Tester", CreatedAt: "2026-11-06 14:00:00", Before: "100.00", After: "110.00", Items: 1}},
	}
	var output bytes.Buffer
	if err := bulkHistoryTemplate.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, snippet := range []string{"История массовых изменений", idTwo, "100.00", "110.00", "/undo", "test-token"} {
		if !strings.Contains(body, snippet) {
			t.Errorf("missing %q", snippet)
		}
	}
	data.CanWrite = false
	output.Reset()
	if err := bulkHistoryTemplate.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "/undo") {
		t.Fatal("read-only user must not see undo form")
	}
}

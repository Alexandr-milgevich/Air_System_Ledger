package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRemapSnapshotKeepsValues(t *testing.T) {
	old := "11111111-1111-1111-1111-111111111111"
	next := "22222222-2222-2222-2222-222222222222"
	got, err := remapSnapshot([]byte(`{"source_item_id":"`+old+`","include_ids":["`+old+`"],"text":"prefix-`+old+`","coefficient":1.5}`), map[string]string{old: next})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err = json.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["source_item_id"] != next || parsed["coefficient"] != 1.5 {
		t.Fatalf("bad mapped snapshot: %s", got)
	}
	if !strings.Contains(parsed["text"].(string), old) {
		t.Fatalf("unexpected partial ID replacement: %s", got)
	}
	arr := parsed["include_ids"].([]any)
	if arr[0] != next {
		t.Fatalf("array not remapped: %s", got)
	}
}
func TestRevisionHistoryTemplate(t *testing.T) {
	var b strings.Builder
	err := revisionDiffTemplate.Execute(&b, revisionDiffView{Source: "11111111-1111-1111-1111-111111111111", Target: "22222222-2222-2222-2222-222222222222", Rows: []revisionDifference{{Code: "П-31", Name: "Воздуховод", Change: "Изменена", BeforeAmount: "100.00", AfterAmount: "110.00"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Воздуховод") || !strings.Contains(b.String(), "110.00") {
		t.Fatal("diff row missing")
	}
}

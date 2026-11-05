package httpserver

import "testing"

func TestDependencyOrder(t *testing.T) {
	items := map[string]*dependencyItem{
		"a": {ID: "a", Mode: "direct", Unit: "m2"},
		"b": {ID: "b", Mode: "sum_multi", Unit: "m2", IncludeIDs: []string{"a"}},
		"c": {ID: "c", Mode: "sum_multi", Unit: "m2", IncludeIDs: []string{"b"}},
	}
	order, err := dependencyOrder(items)
	if err != nil || len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("order=%v err=%v", order, err)
	}
	items["a"].Mode = "sum_multi"
	items["a"].IncludeIDs = []string{"c"}
	if _, err = dependencyOrder(items); err == nil {
		t.Fatal("cycle accepted")
	}
	items["a"].IncludeIDs = []string{"missing"}
	if _, err = dependencyOrder(items); err == nil {
		t.Fatal("missing source accepted")
	}
	items["a"].IncludeIDs = []string{"b", "b"}
	if _, err = dependencyOrder(items); err == nil {
		t.Fatal("duplicate source accepted")
	}
	items["a"].Mode = "direct"
	items["b"].Unit = "pcs"
	if _, err = dependencyOrder(items); err == nil {
		t.Fatal("invalid unit accepted")
	}
}

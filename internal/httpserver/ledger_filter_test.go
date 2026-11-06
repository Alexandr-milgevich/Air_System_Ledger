package httpserver

import "testing"

func TestLedgerFilters(t *testing.T) {
	f, err := parseLedgerFilters("", "  ВозДуХоВоД  ")
	if err != nil || f.Query != "ВозДуХоВоД" {
		t.Fatalf("got %+v, %v", f, err)
	}
	if !ledgerMatches("Монтаж воздуховода", "Коридор", f.Query) {
		t.Fatal("case-insensitive search")
	}
	if !ledgerMatches("Монтаж клапана", "Коридор 1 этаж", "КОРИДОР") {
		t.Fatal("location search")
	}
	if ledgerMatches("Клапан", "Кабинет", "воздуховод") {
		t.Fatal("unexpected match")
	}
	if _, err = parseLedgerFilters("not-uuid", ""); err == nil {
		t.Fatal("accepted invalid system")
	}
	if _, err = parseLedgerFilters("", string(make([]byte, 121))); err == nil {
		t.Fatal("accepted long query")
	}
}

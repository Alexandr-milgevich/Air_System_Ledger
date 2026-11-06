package httpserver

import "testing"

func TestPendingImportIsBoundToUserAndRevision(t *testing.T) {
	token, err := pendingPut("user-a", "revision-a", []byte("xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer pendingDelete(token)
	if _, ok := pendingGet(token, "user-b", "revision-a"); ok {
		t.Fatal("cross-user access")
	}
	if _, ok := pendingGet(token, "user-a", "revision-b"); ok {
		t.Fatal("cross-revision access")
	}
	got, ok := pendingGet(token, "user-a", "revision-a")
	if !ok || string(got) != "xlsx" {
		t.Fatal("missing preview")
	}
}

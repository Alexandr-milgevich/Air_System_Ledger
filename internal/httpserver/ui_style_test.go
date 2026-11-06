package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStage12StylesheetServed(t *testing.T) {
	w := httptest.NewRecorder()
	NewHandler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/app.css", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("css status = %d", w.Code)
	}
	if typ := w.Header().Get("Content-Type"); typ != "text/css; charset=utf-8" {
		t.Fatalf("css type = %q", typ)
	}
	if !strings.Contains(w.Body.String(), ".search-form") {
		t.Fatal("search styles absent")
	}
}

func TestStage12TemplatesIncludeCSS(t *testing.T) {
	for name, body := range map[string]string{"home": page, "workspace": workspaceHTML, "ledger": ledgerHTML, "execution": executionHTML, "reports": reportsHTML, "xlsx": xlsxHTML} {
		if !strings.Contains(body, `href="/assets/app.css"`) {
			t.Errorf("%s missing shared CSS", name)
		}
	}
}

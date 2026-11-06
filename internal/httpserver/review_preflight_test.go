package httpserver

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestReviewPreflightWithLegacyAmount(t *testing.T) {
	db := stage128Pool(t)
	_, _, revision, _, item, _, _ := fixture114(t, db)
	ctx := context.Background()
	good, err := loadReviewPreflight(ctx, db, revision)
	if err != nil {
		t.Fatal(err)
	}
	if !good.Ready || len(good.Issues) != 0 {
		t.Fatalf("expected ready, got %+v", good)
	}

	// Legacy imports can keep an original amount different from quantity * price.
	if _, err := db.Exec(ctx, `UPDATE project_work_items SET amount=2400 WHERE id=$1`, item); err != nil {
		t.Fatal(err)
	}
	bad, err := loadReviewPreflight(ctx, db, revision)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Ready || len(bad.Issues) != 1 {
		t.Fatalf("expected one issue, got %+v", bad)
	}
	if bad.Issues[0].Saved != "2400.00" || bad.Issues[0].Expected != "4800.00" {
		t.Fatalf("unexpected amounts %+v", bad.Issues[0])
	}
}

func TestReviewTemplatePreflightEscapesWorkName(t *testing.T) {
	data := reviewPage{
		Revision: "11111111-1111-1111-1111-111111111111", Status: "draft",
		Checks: reviewPreflight{HasSystems: true, HasItems: true, Issues: []reviewPreflightIssue{{
			System: "В-42", Name: "<script>alert(1)</script>", Quantity: "0.232", Price: "800", Saved: "92.80", Expected: "185.60", Difference: "-92.80",
		}}},
	}
	var out bytes.Buffer
	if err := reviewTemplate.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if strings.Contains(rendered, "<script>") || !strings.Contains(rendered, "&lt;script&gt;") {
		t.Fatal("work name not HTML-escaped")
	}
	if !strings.Contains(rendered, "185.60") || !strings.Contains(rendered, "92.80") {
		t.Fatal("diagnostic amounts missing")
	}
}

package httpserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestStage128TemplateEscapesExceptionReason(t *testing.T) {
	input := reviewPage{
		Revision: "11111111-1111-1111-1111-111111111111",
		Status:   "draft", CanRequest: true,
		Checks: reviewPreflight{HasSystems: true, HasItems: true,
			Issues: []reviewPreflightIssue{{
				ID:     "22222222-2222-2222-2222-222222222222",
				System: "В-42", Name: "Отвод Ø125", Legacy: true,
				Quantity: "0.232", Price: "800", Saved: "92.80", Expected: "185.60",
				Reason:          "<script>alert('unsafe')</script>",
				ExceptionStatus: "Ожидает подтверждения",
			}},
		},
		AmountEvents: []reviewAmountEvent{{Action: "requested", Reason: "<script>bad</script>"}},
	}
	var out bytes.Buffer
	if err := reviewTemplate.Execute(&out, input); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if strings.Contains(rendered, "<script>") || !strings.Contains(rendered, "&lt;script&gt;") {
		t.Fatal("exception data was not HTML escaped")
	}
	if !strings.Contains(rendered, "review/exception/request") {
		t.Fatal("request form missing")
	}
}

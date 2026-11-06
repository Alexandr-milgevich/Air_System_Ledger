package httpserver

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// reviewPreflight is a read-only diagnostic projection. The authoritative
// validation remains inside the status transition transaction.
type reviewPreflight struct {
	HasSystems bool
	HasItems   bool
	HasPending bool
	Ready      bool
	Truncated  bool
	Issues     []reviewPreflightIssue
}

type reviewPreflightIssue struct {
	System                      string
	Name                        string
	Quantity                    string
	Price                       string
	Saved                       string
	Expected                    string
	Difference                  string
	ID, ExceptionStatus, Reason string
	Legacy, Pending             bool
}

func loadReviewPreflight(ctx context.Context, db *pgxpool.Pool, revision string) (reviewPreflight, error) {
	var p reviewPreflight
	var systems, items int
	if err := db.QueryRow(ctx, `
        SELECT (SELECT count(*) FROM project_systems WHERE revision_id=$1),
               (SELECT count(*) FROM project_work_items WHERE revision_id=$1)
    `, revision).Scan(&systems, &items); err != nil {
		return p, fmt.Errorf("preflight counts: %w", err)
	}
	p.HasSystems = systems > 0
	p.HasItems = items > 0

	// Use exactly the same predicate as reviewTransitionValidated. The limit
	// caps HTML output without hiding the fact that there are more violations.
	rows, err := db.Query(ctx, `
        SELECT i.id::text,s.code,i.name_snapshot,
               i.billable_quantity::text, i.unit_price::text,
               i.amount::text,
               round(i.billable_quantity * i.unit_price, 2)::text,
               (i.amount - round(i.billable_quantity * i.unit_price, 2))::text,
               COALESCE((i.rule_snapshot->>'mode'='legacy_snapshot' AND i.rule_snapshot->>'imported_from'='xlsx'),false),
               COALESCE(e.status,''),COALESCE(e.reason,'')
          FROM project_work_items i
          JOIN project_systems s ON s.id=i.system_id AND s.revision_id=i.revision_id
          LEFT JOIN review_amount_exceptions e ON e.revision_id=i.revision_id
           AND e.work_item_id=i.id AND e.calculation_snapshot=review_item_snapshot(i)
         WHERE i.revision_id=$1
           AND (i.amount < 0 OR i.billable_quantity < 0 OR i.unit_price < 0
                OR abs(i.amount - round(i.billable_quantity * i.unit_price,2)) > 0.01)
         ORDER BY s.sort_order, s.code, i.sort_order, i.id
         LIMIT 101
    `, revision)
	if err != nil {
		return p, fmt.Errorf("preflight issues: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var issue reviewPreflightIssue
		if err := rows.Scan(&issue.ID, &issue.System, &issue.Name, &issue.Quantity,
			&issue.Price, &issue.Saved, &issue.Expected, &issue.Difference, &issue.Legacy, &issue.ExceptionStatus, &issue.Reason); err != nil {
			return p, fmt.Errorf("preflight scan: %w", err)
		}
		if len(p.Issues) == 100 {
			p.Truncated = true
			break
		}
		issue.Pending = issue.ExceptionStatus == "pending"
		if issue.Pending {
			p.HasPending = true
		}
		switch issue.ExceptionStatus {
		case "accepted":
			issue.ExceptionStatus = "Подтверждено"
		case "pending":
			issue.ExceptionStatus = "Ожидает подтверждения"
		default:
			issue.ExceptionStatus = "Не оформлено или устарело"
		}
		p.Issues = append(p.Issues, issue)
	}
	if err := rows.Err(); err != nil {
		return p, fmt.Errorf("preflight rows: %w", err)
	}
	p.Ready = p.HasSystems && p.HasItems && !p.Truncated
	for _, issue := range p.Issues {
		if issue.ExceptionStatus == "Не оформлено или устарело" {
			p.Ready = false
		}
	}
	return p, nil
}

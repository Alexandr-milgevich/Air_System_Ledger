package httpserver

import (
	"context"
	"montage-ledger/internal/auth"
)

type dashboardStats struct {
	Projects        int64
	Drafts          int64
	LatestRevisions int64
	EstimateTotal   string
}

// A user sees only member projects, except the global administrator.
// Estimate is the sum from the latest revision of each accessible project,
// not the sum across all historical versions.
func (x web) loadDashboard(ctx context.Context, u auth.Identity) (dashboardStats, error) {
	var d dashboardStats
	admin := auth.HasRole(u, "admin")
	err := x.db.QueryRow(ctx, `
        WITH visible_projects AS (
            SELECT p.id FROM projects p
            WHERE p.archived_at IS NULL
              AND ($2::boolean OR EXISTS (
                  SELECT 1 FROM project_members pm
                  WHERE pm.project_id = p.id AND pm.user_id = $1
              ))
        ), latest AS (
            SELECT vp.id, rev.id AS revision_id, rev.status
            FROM visible_projects vp
            LEFT JOIN LATERAL (
                SELECT r.id, r.status FROM project_revisions r
                WHERE r.project_id = vp.id ORDER BY r.number DESC LIMIT 1
            ) rev ON true
        )
        SELECT (SELECT count(*) FROM visible_projects),
               (SELECT count(*) FROM latest WHERE status = 'draft'),
               (SELECT count(*) FROM latest WHERE revision_id IS NOT NULL)
        `, u.ID, admin).Scan(&d.Projects, &d.Drafts, &d.LatestRevisions)
	if err != nil {
		return d, err
	}
	d.EstimateTotal = "0.00"
	if !admin {
		return d, nil
	}
	err = x.db.QueryRow(ctx, `
        SELECT COALESCE(SUM(i.amount), 0)::text
        FROM projects p
        JOIN LATERAL (
          SELECT id FROM project_revisions
          WHERE project_id=p.id ORDER BY number DESC LIMIT 1
        ) latest ON true
        LEFT JOIN project_work_items i ON i.revision_id = latest.id
        WHERE p.archived_at IS NULL
    `).Scan(&d.EstimateTotal)
	return d, err
}

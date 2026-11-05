package httpserver

import (
	"context"
	"errors"
	"fmt"
	"montage-ledger/internal/calculation"

	"github.com/jackc/pgx/v5"
)

type dependencyItem struct {
	ID, SourceID, Mode, Quantity, Unit, Coefficient, Price, Calculated string
}

// linkedCycle checks whether a proposed source references the edited item transitively.
func linkedCycle(ctx context.Context, tx pgx.Tx, revisionID, itemID, sourceID string) (bool, error) {
	if itemID == sourceID {
		return true, nil
	}
	seen := map[string]bool{}
	current := sourceID
	for current != "" {
		if current == itemID {
			return true, nil
		}
		if seen[current] {
			return true, nil
		}
		seen[current] = true
		var next string
		err := tx.QueryRow(ctx, `SELECT COALESCE(inputs_snapshot->>'source_item_id','') FROM project_work_items WHERE revision_id=$1 AND id=$2`, revisionID, current).Scan(&next)
		if err != nil {
			return false, err
		}
		current = next
	}
	return false, nil
}

// refreshDependents updates saved derived quantities in topological order within the caller's transaction.
// A source must be in the same revision and must produce m²; cycles roll back the whole edit.
func refreshDependents(ctx context.Context, tx pgx.Tx, revisionID string) error {
	rows, err := tx.Query(ctx, `SELECT id::text, COALESCE(inputs_snapshot->>'source_item_id',''), COALESCE(rule_snapshot->>'mode','direct'),input_quantity::text,result_unit_code,COALESCE(coefficient::text,''),unit_price::text,calculated_quantity::text FROM project_work_items WHERE revision_id=$1`, revisionID)
	if err != nil {
		return err
	}
	items := map[string]*dependencyItem{}
	for rows.Next() {
		var it dependencyItem
		if err = rows.Scan(&it.ID, &it.SourceID, &it.Mode, &it.Quantity, &it.Unit, &it.Coefficient, &it.Price, &it.Calculated); err != nil {
			break
		}
		items[it.ID] = &it
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	marks := map[string]int{}
	var ordered []string
	var visit func(string) error
	visit = func(id string) error {
		if marks[id] == 2 {
			return nil
		}
		if marks[id] == 1 {
			return errors.New("dependency cycle")
		}
		it, ok := items[id]
		if !ok {
			return errors.New("missing dependency")
		}
		marks[id] = 1
		if it.Mode == "sum_items" {
			if it.SourceID == "" {
				return errors.New("missing source for linked item")
			}
			if err := visit(it.SourceID); err != nil {
				return err
			}
		}
		marks[id] = 2
		ordered = append(ordered, id)
		return nil
	}
	for id := range items {
		if err := visit(id); err != nil {
			return err
		}
	}
	for _, id := range ordered {
		it := items[id]
		if it.Mode != "sum_items" {
			continue
		}
		src := items[it.SourceID]
		if src.Unit != "m2" {
			return fmt.Errorf("source %s must have m2 result", src.ID)
		}
		r, e := calculation.Linked(src.Calculated, it.Coefficient, it.Price)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE project_work_items SET calculated_quantity=$3,billable_quantity=$3,amount=$4 WHERE revision_id=$1 AND id=$2`, revisionID, id, r.Calculated, r.Amount)
		if e != nil {
			return e
		}
		it.Calculated = r.Calculated
	}
	return nil
}

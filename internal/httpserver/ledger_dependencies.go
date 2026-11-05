package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"montage-ledger/internal/calculation"
)

type dependencyItem struct {
	ID, SourceID, Mode, Quantity, Unit, Coefficient, Price, Calculated string
	IncludeIDs, ExcludeIDs                                             []string
}

func dependencies(it *dependencyItem) []string {
	switch it.Mode {
	case "sum_items":
		if it.SourceID != "" {
			return []string{it.SourceID}
		}
	case "sum_multi":
		return append(append([]string{}, it.IncludeIDs...), it.ExcludeIDs...)
	}
	return nil
}

// dependencyOrder validates every edge and returns a deterministic topological order.
func dependencyOrder(items map[string]*dependencyItem) ([]string, error) {
	state := make(map[string]uint8, len(items))
	order := make([]string, 0, len(items))
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 2 {
			return nil
		}
		if state[id] == 1 {
			return fmt.Errorf("dependency cycle at %s", id)
		}
		it, ok := items[id]
		if !ok {
			return fmt.Errorf("missing referenced item %s", id)
		}
		state[id] = 1
		used := map[string]bool{}
		for _, dep := range dependencies(it) {
			if used[dep] {
				return fmt.Errorf("duplicate dependency %s", dep)
			}
			used[dep] = true
			src, found := items[dep]
			if !found {
				return fmt.Errorf("missing referenced item %s", dep)
			}
			if src.Unit != "m2" {
				return fmt.Errorf("source %s must produce m2", dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		order = append(order, id)
		return nil
	}
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := visit(k); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// linkedCycle reads all revision edges, including multi-source dependencies.
func linkedCycle(ctx context.Context, tx pgx.Tx, revisionID, itemID, sourceID string) (bool, error) {
	items, err := loadDependencyItems(ctx, tx, revisionID)
	if err != nil {
		return false, err
	}
	if itemID == sourceID {
		return true, nil
	}
	if _, ok := items[sourceID]; !ok {
		return false, errors.New("missing source")
	}
	if it, ok := items[itemID]; ok {
		it.Mode = "sum_items"
		it.SourceID = sourceID
	}
	_, err = dependencyOrder(items)
	if err != nil {
		return true, nil
	}
	return false, nil
}

func loadDependencyItems(ctx context.Context, tx pgx.Tx, revisionID string) (map[string]*dependencyItem, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, COALESCE(inputs_snapshot->>'source_item_id',''), COALESCE(rule_snapshot->>'mode','direct'), input_quantity::text,result_unit_code,COALESCE(coefficient::text,''),unit_price::text,calculated_quantity::text, (CASE WHEN jsonb_typeof(inputs_snapshot->'include_ids')='array' THEN inputs_snapshot->'include_ids' ELSE '[]'::jsonb END)::text,(CASE WHEN jsonb_typeof(inputs_snapshot->'exclude_ids')='array' THEN inputs_snapshot->'exclude_ids' ELSE '[]'::jsonb END)::text FROM project_work_items WHERE revision_id=$1`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]*dependencyItem{}
	for rows.Next() {
		var it dependencyItem
		var incl, excl string
		if err = rows.Scan(&it.ID, &it.SourceID, &it.Mode, &it.Quantity, &it.Unit, &it.Coefficient, &it.Price, &it.Calculated, &incl, &excl); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(incl), &it.IncludeIDs); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(excl), &it.ExcludeIDs); err != nil {
			return nil, err
		}
		items[it.ID] = &it
	}
	return items, rows.Err()
}

// refreshDependents recomputes all derived nodes atomically in the editing transaction.
func refreshDependents(ctx context.Context, tx pgx.Tx, revisionID string) error {
	items, err := loadDependencyItems(ctx, tx, revisionID)
	if err != nil {
		return err
	}
	order, err := dependencyOrder(items)
	if err != nil {
		return err
	}
	for _, id := range order {
		it := items[id]
		var r calculation.Result
		switch it.Mode {
		case "sum_items":
			if it.SourceID == "" {
				return errors.New("missing legacy source")
			}
			r, err = calculation.Linked(items[it.SourceID].Calculated, it.Coefficient, it.Price)
		case "sum_multi":
			incl := make([]string, 0, len(it.IncludeIDs))
			excl := make([]string, 0, len(it.ExcludeIDs))
			for _, s := range it.IncludeIDs {
				incl = append(incl, items[s].Calculated)
			}
			for _, s := range it.ExcludeIDs {
				excl = append(excl, items[s].Calculated)
			}
			r, err = calculation.AggregateArea(incl, excl, it.Coefficient, it.Price)
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("item %s: %w", id, err)
		}
		_, err = tx.Exec(ctx, `UPDATE project_work_items SET calculated_quantity=$3,billable_quantity=$3,amount=$4,trace_snapshot=jsonb_build_object('mode',$5::text,'rule_version',2,'calculated_quantity',$3::text,'amount',$4::text) WHERE revision_id=$1 AND id=$2`, revisionID, id, r.Calculated, r.Amount, it.Mode)
		if err != nil {
			return err
		}
		it.Calculated = r.Calculated
	}
	return nil
}

package ingest

import (
	"context"
	"encoding/json"

	"claude-trace/internal/store"
)

// BackfillBashClassification populates bash_program / bash_action for every
// existing Bash tool_call that doesn't have them yet. Safe to run repeatedly;
// only visits rows where bash_program IS NULL.
func BackfillBashClassification(ctx context.Context, st *store.Store) (int, error) {
	db := st.DB()
	// Include rows we previously (mis)classified as a shell keyword or comment —
	// the improved parser now walks past those to the real program.
	rows, err := db.Query(`
		SELECT id, input_json FROM tool_calls
		WHERE is_bash = 1
		  AND (bash_program IS NULL OR bash_program = ''
		       OR bash_program IN ('#','for','while','until','if','case','do','done','then','else','fi','esac','function','in','time'))
	`)
	if err != nil {
		return 0, err
	}
	type row struct{ ID int64; Input string }
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Input); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()

	updated := 0
	for _, r := range todo {
		select {
		case <-ctx.Done():
			return updated, ctx.Err()
		default:
		}
		prog, act := ClassifyBashInput(json.RawMessage(r.Input))
		if prog == "" {
			continue
		}
		if _, err := db.Exec(`UPDATE tool_calls SET bash_program = ?, bash_action = ? WHERE id = ?`,
			prog, act, r.ID); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

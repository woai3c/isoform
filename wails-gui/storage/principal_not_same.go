package storage

import (
	"context"
	"errors"
	"time"
)

// notSamePairs lists every pair of ids once, in order.
func notSamePairs(ids []string) [][2]string {
	var pairs [][2]string
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			switch true {
			case a == b:
			case a < b:
				pairs = append(pairs, [2]string{a, b})
			default:
				pairs = append(pairs, [2]string{b, a})
			}
		}
	}
	return pairs
}

// MarkPeopleNotSame records that these profiles are different people.
func (s *Store) MarkPeopleNotSame(ctx context.Context, ids []string) error {
	pairs := notSamePairs(ids)
	if len(pairs) == 0 {
		return errors.New("not the same needs two profiles")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO principal_not_same(a,b,created_at) VALUES(?,?,?)`, pair[0], pair[1], now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UndoPeopleNotSame takes the answer back, so the profiles may be suggested
// again.
func (s *Store) UndoPeopleNotSame(ctx context.Context, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, pair := range notSamePairs(ids) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM principal_not_same WHERE a=? AND b=?`, pair[0], pair[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PeopleNotSame reports, for the merge suggester, whether every pair in ids
// was said to be different people.
func (s *Store) PeopleNotSame(ctx context.Context) (func(ids []string) bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a,b FROM principal_not_same`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[[2]string]bool{}
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			return nil, err
		}
		known[pair] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return func(ids []string) bool {
		pairs := notSamePairs(ids)
		for _, pair := range pairs {
			if !known[pair] {
				return false
			}
		}
		return len(pairs) > 0
	}, nil
}

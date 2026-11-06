package storage

import (
	"context"
	"time"
)

// EngagementNotification is one moment's first delivery on an interrupting
// channel. Multiple devices, retries and chat records do not add new moments.
type EngagementNotification struct {
	MomentID      string
	Kind          string
	At            int64
	Acted         bool
	OpenedOrActed bool
	Useful        bool
}

func (s *Store) EngagementNotificationsBetween(ctx context.Context, since, until time.Time) ([]EngagementNotification, error) {
	rows, err := s.db.QueryContext(ctx, `
        WITH first_notice AS (
            SELECT moment_id, MIN(at) AS at FROM engagement_receipts
            WHERE channel IN ('desktop','push','text','email') AND state IN ('sent','delivered')
            GROUP BY moment_id
        )
        SELECT m.id, m.kind, n.at, EXISTS(
            SELECT 1 FROM engagement_receipts a WHERE a.moment_id=m.id
              AND a.channel=? AND a.state='acted' AND a.action=? AND a.at>=n.at AND a.at<=?
        ), EXISTS(
            SELECT 1 FROM engagement_receipts a WHERE a.moment_id=m.id
              AND a.state IN ('opened','acted') AND a.at>=n.at AND a.at<=?
        ), EXISTS(
            SELECT 1 FROM engagement_receipts a WHERE a.moment_id=m.id AND a.at>=n.at AND a.at<=?
              AND CASE WHEN m.kind IN ('brief_ready','meeting_ended','weekly_recap')
                       THEN (a.state='opened' OR (a.state='acted' AND (a.channel!=? OR a.action!=?))) ELSE a.state='acted' END
        )
        FROM first_notice n JOIN engagement_moments m ON m.id=n.moment_id
        WHERE n.at BETWEEN ? AND ? ORDER BY n.at, m.id
    `, EngagementCompletionChannel, EngagementCompletionAction, until.UnixMilli(), until.UnixMilli(), until.UnixMilli(), EngagementCompletionChannel, EngagementCompletionAction, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []EngagementNotification
	for rows.Next() {
		var item EngagementNotification
		if err := rows.Scan(&item.MomentID, &item.Kind, &item.At, &item.Acted, &item.OpenedOrActed, &item.Useful); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

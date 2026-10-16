package scheduler

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// closeEndedSupportVisits runs before token retention deletes. Idle time is never a termination.
func closeEndedSupportVisits(ctx context.Context, b db.Beginner, now time.Time) (int64, error) {
	var users []uuid.UUID
	err := db.WithSystem(ctx, b, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, supportListVisitingUsersSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var uid uuid.UUID
			if err := rows.Scan(&uid); err != nil {
				return err
			}
			users = append(users, uid)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	var closed int64
	for _, uid := range users {
		err = db.WithSystem(ctx, b, nil, func(tx pgx.Tx) error {
			// Match auth's user -> session -> visit order, including refresh/expiry races.
			if _, err := tx.Exec(ctx, supportLockVisitUserSQL, uid); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, supportLockVisitSessionsSQL, uid)
			if err != nil {
				return err
			}
			type ended struct {
				id     uuid.UUID
				at     time.Time
				reason string
			}
			var ends []ended
			for rows.Next() {
				var sid uuid.UUID
				var revoked, refresh, roleRemoved *time.Time
				var absolute time.Time
				var reason *string
				if err := rows.Scan(&sid, &revoked, &reason, &absolute, &refresh, &roleRemoved); err != nil {
					rows.Close()
					return err
				}
				at, why := absolute, db.SupportVisitExpired
				if refresh != nil && refresh.Before(at) {
					at = *refresh
				}
				if revoked != nil && !revoked.After(at) {
					at = *revoked
					why = db.SupportVisitRevoked
					if reason != nil && (*reason == auth.RevokeLogout || *reason == auth.RevokeLogoutAll) {
						why = db.SupportVisitLogout
					}
				}
				if roleRemoved != nil && roleRemoved.Before(at) {
					at = *roleRemoved
					why = db.SupportVisitRoleRemoved
				}
				if !at.After(now) {
					ends = append(ends, ended{sid, at, why})
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, e := range ends {
				if err := auth.CloseSupportVisit(ctx, tx, e.id, e.reason, "", e.at, now); err != nil {
					return err
				}
				closed++
			}
			return nil
		})
		if err != nil {
			return closed, err
		}
	}
	return closed, nil
}

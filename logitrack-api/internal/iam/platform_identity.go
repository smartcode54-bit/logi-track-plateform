package iam

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A person is attributable only to one tenant. Platform and shared accounts have no tenant owner.
func attributableUserOwners(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := tx.Query(ctx, `SELECT u.id, CASE WHEN count(DISTINCT m.tenant_id)=1 AND NOT EXISTS(SELECT 1 FROM user_platform_roles p WHERE p.user_id=u.id) THEN min(m.tenant_id::text) ELSE '' END FROM users u LEFT JOIN memberships m ON m.user_id=u.id WHERE u.id=ANY($1::uuid[]) GROUP BY u.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var owner string
		if err := rows.Scan(&id, &owner); err != nil {
			return nil, err
		}
		owners[id] = owner
	}
	return owners, rows.Err()
}
func derefUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

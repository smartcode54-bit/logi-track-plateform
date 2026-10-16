package iam

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
)

// attributableUserOwners returns the sole membership's tenant ID, PlatformOwner for platform
// staff without memberships or scopes, or an empty string for shared or unknown ownership (masked).
func attributableUserOwners(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := tx.Query(ctx, `SELECT u.id, CASE
		WHEN count(DISTINCT m.tenant_id)=1
			THEN min(m.tenant_id::text)
		WHEN count(DISTINCT m.tenant_id)=0 AND NOT EXISTS(SELECT 1 FROM user_scopes s WHERE s.user_id=u.id)
			AND EXISTS(SELECT 1 FROM user_platform_roles p WHERE p.user_id=u.id AND p.role IN ('platform_admin','support'))
			THEN $2::text
		ELSE '' END
		FROM users u LEFT JOIN memberships m ON m.user_id=u.id WHERE u.id=ANY($1::uuid[]) GROUP BY u.id`, ids, supportprojection.PlatformOwner)
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

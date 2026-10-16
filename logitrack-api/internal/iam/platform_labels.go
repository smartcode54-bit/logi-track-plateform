package iam

import (
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

type platformLabel struct {
	kind, legal, nameTh string
	code, nameEn        *string
	enabled, reviewed   bool
	version             int64
}

// Organization labels are public structure; individual/unknown carrier labels and unreviewed codes
// are projected here. The final serializer checks this exact label version under its tenant lock.
func (a *Admin) projectTenantLabels(c call, t *Tenant) error {
	if !c.p.IsPlatform() && !authz.IsSupport(c.p) {
		return nil
	}
	labels := c.labels
	if labels == nil {
		labels = map[uuid.UUID]platformLabel{}
	}
	label, ok := labels[t.ID]
	if !ok {
		err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
			return tx.QueryRow(c.ctx, `SELECT kind,coalesce(legal_type,''),name_th,name_en,code::text,support_access_enabled,support_code_reviewed,support_label_version FROM tenants WHERE id=$1`, t.ID).Scan(&label.kind, &label.legal, &label.nameTh, &label.nameEn, &label.code, &label.enabled, &label.reviewed, &label.version)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		labels[t.ID] = label
	}
	t.NameTh, t.NameEn, t.Code = label.nameTh, label.nameEn, label.code
	if !label.enabled {
		if label.kind == "carrier" && label.legal != "company" {
			masked := "****"
			t.NameTh, t.NameEn = masked, &masked
		}
		if !label.reviewed {
			t.Code = nil
		}
	}
	if c.labelVersions != nil {
		c.labelVersions[t.ID] = label.version
	}
	return nil
}
func (a *Admin) projectUserLabels(c call, users []User) error {
	if !c.p.IsPlatform() && !authz.IsSupport(c.p) {
		return nil
	}
	ids := []uuid.UUID{}
	for _, u := range users {
		for _, m := range u.Memberships {
			ids = append(ids, m.TenantID)
		}
	}
	var err error
	c.labels, err = a.loadPlatformLabels(c, ids)
	if err != nil {
		return err
	}
	for i := range users {
		for j := range users[i].Memberships {
			m := &users[i].Memberships[j]
			t := Tenant{ID: m.TenantID}
			if err := a.projectTenantLabels(c, &t); err != nil {
				return err
			}
			m.TenantNameTh, m.TenantNameEn = t.NameTh, t.NameEn
		}
	}
	return nil
}

func (a *Admin) loadPlatformLabels(c call, ids []uuid.UUID) (map[uuid.UUID]platformLabel, error) {
	labels := c.labels
	if labels == nil {
		labels = map[uuid.UUID]platformLabel{}
	}
	missing := []uuid.UUID{}
	for _, id := range ids {
		if _, ok := labels[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return labels, nil
	}
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(c.ctx, `SELECT id,kind,coalesce(legal_type,''),name_th,name_en,code::text,support_access_enabled,support_code_reviewed,support_label_version FROM tenants WHERE id=ANY($1::uuid[])`, missing)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var l platformLabel
			if err := rows.Scan(&id, &l.kind, &l.legal, &l.nameTh, &l.nameEn, &l.code, &l.enabled, &l.reviewed, &l.version); err != nil {
				return err
			}
			labels[id] = l
		}
		return rows.Err()
	})
	return labels, err
}

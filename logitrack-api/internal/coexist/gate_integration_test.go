//go:build integration

package coexist

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// lastSettingsChanged returns the newest outbox settings.changed as the relay hands it to firestore.shadow.
func (f *fixture) lastSettingsChanged(t *testing.T) *mq.Delivery {
	t.Helper()
	var d mq.Delivery
	if err := db.WithSystem(f.ctx, f.app, nil, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctx, `SELECT routing_key, payload, created_at FROM outbox_events
			WHERE routing_key = $1 ORDER BY id DESC LIMIT 1`, mobileapp.RouteSettingsChanged).Scan(&d.RoutingKey, &d.Body, &d.OccurredAt)
	}); err != nil {
		t.Fatal(err)
	}
	d.Queue = QueueShadow
	return &d
}

// AC (T52R, issue #72): a version floor set in Go reaches installed 3.x APKs. PUT /v1/app-releases/floor (the
// mobileapp service, as a person) writes the floor half of settings('mobile_app') and the outbox settings.changed;
// firestore.shadow, as logitrack_app in db.WithSystem, projects settings/mobile_app with the floor next to its
// download link, and removes the floor when it is removed. While mobile_release is Firestore-owned the event projects
// nothing (counted not_projected).
func TestShadowProjectsTheVersionFloorSetInGo(t *testing.T) {
	f := newFixture(t, true)
	reg := prometheus.NewRegistry()
	s, err := NewShadow(ShadowConfig{Enabled: true, Pool: f.app, FS: f.fs.Firestore(), Plan: plan(t, "auth,masterdata,mobile_release"),
		Log: zerolog.Nop(), Metrics: NewMetrics(reg)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := mobileapp.New(mobileapp.Deps{Pool: f.app, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `INSERT INTO users (email, display_name) VALUES ('owner@example.test', 'Owner') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	person := &authz.Principal{UserID: owner, AMR: authz.AMRPassword}
	setFloor := func(floor string) {
		t.Helper()
		res, err := svc.SetFloor(f.ctx, person, mobileapp.FloorInput{MinAllowedVersion: json.RawMessage(floor)}, "req-t52r")
		if err != nil || !res.Changed {
			t.Fatalf("set floor %s: %+v %v", floor, res, err)
		}
	}
	link, _ := f.fs.Doc("settings/mobile_app")

	// The fixture's legacy gate: floor 3.1.0, latest 3.2.0. The owner raises the floor to the latest release.
	setFloor(`"3.2.0"`)
	if err := s.Handle(f.ctx, f.lastSettingsChanged(t)); err != nil {
		t.Fatal(err)
	}
	doc, _ := f.fs.Doc("settings/mobile_app")
	if doc.Fields["minAllowedVersion"] != "3.2.0" || doc.Fields["minAllowedVersionSetBy"] != "owner@example.test" ||
		doc.Fields["apkDownloadUrl"] != link.Fields["apkDownloadUrl"] || doc.Fields["apkDownloadUrl"] == "" {
		t.Fatalf("the floor did not reach the 3.x gate: %v", doc.Fields)
	}

	// Removing the floor in Go lifts it on the devices too.
	setFloor(`null`)
	if err := s.Handle(f.ctx, f.lastSettingsChanged(t)); err != nil {
		t.Fatal(err)
	}
	doc, _ = f.fs.Doc("settings/mobile_app")
	if _, ok := doc.Fields["minAllowedVersion"]; ok {
		t.Fatalf("the removed floor stays in Firestore: %v", doc.Fields)
	}
	if v := counter(t, reg, "projection_writes_total", "settings", "written"); v != 2 {
		t.Errorf("projection_writes_total{settings,written} %v", v)
	}

	// Before P6 the gate is Firestore's: the same event writes nothing.
	before, err := NewShadow(ShadowConfig{Enabled: true, Pool: f.app, FS: f.fs.Firestore(), Plan: plan(t, "auth,masterdata"), Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	setFloor(`"3.2.0"`)
	writes := len(f.fs.Writes())
	if err := before.Handle(f.ctx, f.lastSettingsChanged(t)); err != nil {
		t.Fatal(err)
	}
	if len(f.fs.Writes()) != writes {
		t.Error("a Firestore-owned gate was projected")
	}
}

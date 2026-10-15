package seed

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
)

func TestFleetDemoTruckWarningCoverage(t *testing.T) {
	p := plan(t, ProfileDemo)
	var trucks int
	for _, truck := range p.Rows("trucks") {
		if !strings.HasPrefix(truck.Str("license_plate"), "9กข-41") {
			continue
		}
		trucks++
		// All nine trucks have maintenance history. A top-up must warn if an existing
		// row keeps conflicting service state beside the records the top-up inserts.
		for _, column := range []string{"status", "current_mileage", "next_service_mileage", "active_maintenance_id", "last_service_date"} {
			if !slices.Contains(p.truckChanges[truck.Str("id")], column) {
				t.Errorf("%s: kept-truck warning does not check %s", truck.Str("license_plate"), column)
			}
		}
	}
	if trucks != 9 {
		t.Fatalf("checked %d fleet cohort trucks, want 9", trucks)
	}
}

func TestFleetDemoFuelType(t *testing.T) {
	for _, truck := range plan(t, ProfileDemo).Rows("trucks") {
		if strings.HasPrefix(truck.Str("license_plate"), "9กข-41") && truck.Str("fuel_type") != "Diesel" {
			t.Errorf("%s: fuel type %q does not match the truck form's Diesel option", truck.Str("license_plate"), truck.Str("fuel_type"))
		}
	}
}

func TestFleetDemoDriverIdentity(t *testing.T) {
	p := plan(t, ProfileDemo)
	thai, romanized, births := map[string]int{}, map[string]int{}, map[string]bool{}
	for _, driver := range p.Rows("drivers") {
		thai[driver.Str("full_name_th")]++
		romanized[driver.Str("first_name")+" "+driver.Str("last_name")]++
	}
	var drivers int
	for _, driver := range p.Rows("drivers") {
		if !strings.HasPrefix(driver.Str("id_card"), "1999900041") {
			continue
		}
		drivers++
		th, en, birth := driver.Str("full_name_th"), driver.Str("first_name")+" "+driver.Str("last_name"), driver.Str("birth_date")
		if thai[th] != 1 || romanized[en] != 1 {
			t.Errorf("fleet cohort repeats a driver name: %s / %s", th, en)
		}
		if _, err := time.Parse(time.DateOnly, birth); err != nil || births[birth] {
			t.Errorf("%s: invalid or repeated birth date %q", th, birth)
		}
		births[birth] = true
	}
	if drivers != 6 {
		t.Fatalf("checked %d fleet cohort drivers, want 6", drivers)
	}
}

func TestFleetCoherence(t *testing.T) {
	for _, profile := range []Profile{ProfileSmoke, ProfileDemo, ProfileLoad} {
		t.Run(string(profile), func(t *testing.T) {
			p := plan(t, profile)
			trucks, drivers, records := byID(p.Rows("trucks")), byID(p.Rows("drivers")), byID(p.Rows("maintenance_records"))
			for _, truck := range trucks {
				if truck.Str("status") != "maintenance" {
					continue
				}
				r := records[truck.Str("active_maintenance_id")]
				if r == nil || !openStatus(r.Str("status")) || r.Str("truck_id") != truck.Str("id") || r.Str("tenant_id") != truck.Str("tenant_id") {
					t.Errorf("maintenance truck %s has no linked open record", truck.Str("license_plate"))
				}
			}
			for _, a := range p.Rows("truck_assignments") {
				if a.Str("status") != "active" {
					continue
				}
				d := drivers[a.Str("driver_id")]
				if d.Str("status") != "on_duty" || d.Str("current_assignment_id") != a.Str("id") {
					t.Errorf("assigned driver %s is not on duty with the current assignment", d.Str("full_name_th"))
				}
			}
		})
	}
}

func TestFleetDemoOnlyAppends(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom-namespace-and-anchor=%v", custom), func(t *testing.T) {
			o := Options{Profile: ProfileDemo, Namespace: DefaultNamespace, RandomSeed: DefaultRandomSeed, Anchor: DefaultAnchor,
				Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "s3"}
			if custom {
				o.Namespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
				own := uuid.MustParse("0198f0aa-0000-7000-8000-000000000001")
				o.OwnFleet, o.RandomSeed, o.Anchor = &own, integrationSeed, DefaultAnchor.AddDate(0, 0, 11)
			}
			// Rebuild the older demo generators independently of the new cohort.
			smoke := o
			smoke.Profile = ProfileSmoke
			p, err := Build(context.Background(), testFixture(), smoke)
			if err != nil {
				t.Fatal(err)
			}
			p.Profile = ProfileDemo
			addDemo(p, o)
			for _, generate := range []func(*Plan, Options) error{addDemoMaster, addDemoOps, addDemoHR, addDemoFinance} {
				if err := generate(p, o); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string][]Row{}
			for table, rows := range p.Tables {
				for _, r := range rows {
					before[table] = append(before[table], r.clone())
				}
			}
			if err := addFleetDemo(p, o); err != nil {
				t.Fatal(err)
			}
			want := map[string]int{"tenants": 2, "trucks": 9, "drivers": 6, "truck_assignments": 9,
				"maintenance_records": 12, "file_objects": 2, "tenant_files": 2}
			for _, table := range LoadOrder {
				old, rows := before[table], p.Rows(table)
				if len(rows)-len(old) != want[table] {
					t.Errorf("%s adds %d rows, want %d", table, len(rows)-len(old), want[table])
				}
				if len(old) > 0 && !reflect.DeepEqual(old, rows[:len(old)]) {
					t.Errorf("fleet cohort changed an existing %s row", table)
				}
				for _, r := range rows[len(old):] {
					// Exercise the real insert serializer, including PostgreSQL text arrays and insert-only SQL.
					cols := map[string]column{}
					for key, value := range r {
						cols[key] = column{typ: "text", array: true}
						if _, isComputed := computed(value); isComputed {
							r = r.clone()
							r[key] = "placeholder bytes are drawn before loading"
						}
					}
					q, _, err := insertSQL(table, r, cols, ModeUpsert)
					if err != nil || !strings.HasSuffix(q, " ON CONFLICT DO NOTHING") {
						t.Errorf("%s cannot be inserted as a top-up: %v", table, err)
					}
				}
			}
			if err := validate(p); err != nil {
				t.Fatal(err)
			}
			checkFleet(t, p)
			checkAssignmentSpans(t, p)
			if err := priceOperations(p); err != nil {
				t.Fatal(err)
			}
			for _, finish := range []func(*Plan, Options) error{addStatements, objects} {
				if err := finish(p, o); err != nil {
					t.Fatal(err)
				}
			}
			built := buildPlan(t, o)
			if !reflect.DeepEqual(p.Tables, built.Tables) || !reflect.DeepEqual(built.Tables, buildPlan(t, o).Tables) {
				t.Error("the production plan differs from the preserved older rows plus the deterministic cohort")
			}
		})
	}
}

func TestFleetDemoCarrierDocumentsAndHistory(t *testing.T) {
	p := plan(t, ProfileDemo)
	trucks, drivers, files := byID(p.Rows("trucks")), byID(p.Rows("drivers")), byID(p.Rows("file_objects"))
	for _, tenant := range p.Rows("tenants") {
		if !strings.HasPrefix(tenant.Str("code"), "DEMO-") {
			continue
		}
		id := tenant.Str("id")
		regions, ok := tenant["service_regions"].([]any)
		if !ok || len(regions) < 2 || tenant.Str("contractor_tenant_id") != p.Symbols.MustID("TN_OWN").String() {
			t.Errorf("%s has no service regions or own-fleet link", tenant.Str("code"))
		}
		var fleet, people, active, revoked, documents int
		for _, r := range trucks {
			if r.Str("tenant_id") == id {
				fleet++
			}
		}
		for _, r := range drivers {
			if r.Str("tenant_id") == id {
				people++
			}
		}
		for _, r := range p.Rows("truck_assignments") {
			if r.Str("tenant_id") == id {
				if r.Str("status") == "active" {
					active++
				} else if r.Str("status") == "revoked" && r.Str("revoked_at") != "" {
					revoked++
				}
			}
		}
		for _, link := range p.Rows("tenant_files") {
			if link.Str("tenant_id") != id {
				continue
			}
			file := files[link.Str("file_id")]
			if file == nil || file.Str("tenant_id") != id || file.Str("owner_id") != id || file.Str("purpose") != "tenant_document" || file.Str("status") != "committed" {
				t.Errorf("%s has an invalid carrier document", tenant.Str("code"))
			}
			documents++
		}
		if declared, _ := intOf(tenant["fleet_size"]); declared != fleet || fleet != 3 || people != 2 || active != 2 || revoked != 1 || documents != 1 {
			t.Errorf("%s: fleet=%d drivers=%d active=%d revoked=%d documents=%d", tenant.Str("code"), fleet, people, active, revoked, documents)
		}
	}
}

func TestFleetDemoExpiryAndPMSpread(t *testing.T) {
	for name, c := range demoCases {
		t.Run(name, func(t *testing.T) {
			p := buildPlan(t, Options{Profile: ProfileDemo, RandomSeed: c.seed, Anchor: c.anchor})
			anchor := c.anchor
			if anchor.IsZero() {
				anchor = DefaultAnchor
			}
			for _, code := range []string{"WRT", "DEMO-EAST", "DEMO-CENTRAL"} {
				var tenant string
				for _, r := range p.Rows("tenants") {
					if r.Str("code") == code {
						tenant = r.Str("id")
					}
				}
				if tenant == "" {
					t.Errorf("missing demo tenant %s", code)
					continue
				}
				spread := map[string][3]int{}
				pm := [3]int{}
				for _, truck := range p.Rows("trucks") {
					if truck.Str("tenant_id") != tenant || !strings.HasPrefix(truck.Str("license_plate"), "9กข-41") {
						continue
					}
					registered, err := time.Parse(time.DateOnly, truck.Str("registration_date"))
					if err != nil || registered.After(anchor) {
						t.Errorf("%s has no valid registration date", truck.Str("license_plate"))
					}
					for _, column := range []string{"tax_expiry_date", "insurance_expiry_date"} {
						due, err := time.Parse(time.DateOnly, truck.Str(column))
						if err != nil {
							continue
						}
						days := int(due.Sub(time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
						n := 2
						if days < 0 {
							n = 0
						} else if days <= 30 {
							n = 1
						}
						v := spread[column]
						v[n]++
						spread[column] = v
					}
					current, ok := intOf(truck["current_mileage"])
					next, scheduled := intOf(truck["next_service_mileage"])
					if ok && scheduled {
						switch left := next - current; {
						case left < 0:
							pm[0]++
						case left <= maintenance.PMAlertThresholdKm:
							pm[1]++
						default:
							pm[2]++
						}
					}
				}
				for _, column := range []string{"tax_expiry_date", "insurance_expiry_date"} {
					v := spread[column]
					if v[0] == 0 || v[1] == 0 || v[2] == 0 {
						t.Errorf("%s %s lacks overdue/soon/later spread: %v", code, column, v)
					}
				}
				if pm[0] == 0 || pm[1] == 0 || pm[2] == 0 {
					t.Errorf("%s lacks overdue/soon/ok PM spread: %v", code, pm)
				}
			}
		})
	}
}

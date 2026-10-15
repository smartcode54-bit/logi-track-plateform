package seed

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// addFleetDemo appends an independent fleet cohort after the older generators. Its natural keys and
// dates do not change earlier rows or random streams, so shared deployments can top it up insert-only.
func addFleetDemo(p *Plan, o Options) error {
	g := newMasterGen(p, o)
	own, admin := p.Symbols.MustID("TN_OWN").String(), p.Symbols.MustID("U_WRT_ADMIN").String()
	day := func(n int) time.Time { return o.Anchor.AddDate(0, 0, n) }
	created := day(-75).Add(9 * time.Hour)
	changed := map[string][]string{}
	tenants := []*tenantRef{g.tenant(own, "WRT", true, admin)}
	for _, s := range []struct {
		code, th, en, province string
		regions                []any
	}{
		{"DEMO-EAST", "บจก. ขนส่งตะวันออก (ตัวอย่าง)", "Eastern Transport (sample)", "ชลบุรี", []any{"ชลบุรี", "ระยอง", "ฉะเชิงเทรา"}},
		{"DEMO-CENTRAL", "บจก. ขนส่งภาคกลาง (ตัวอย่าง)", "Central Transport (sample)", "พระนครศรีอยุธยา", []any{"กรุงเทพมหานคร", "ปทุมธานี", "พระนครศรีอยุธยา"}},
	} {
		id := g.id("tenants", s.code)
		g.add("tenants", Row{"id": id, "kind": "carrier", "code": s.code, "legal_type": "company",
			"name_th": s.th, "name_en": s.en, "tax_id": thaiID(fmt.Sprintf("0999900041%02d", len(tenants))),
			"contact_person": "สมศักดิ์ ใจดี", "phone": fmt.Sprintf("000-000-04%02d", len(tenants)),
			"email": fmt.Sprintf("fleet.demo%d@logitrack.test", len(tenants)), "address": "ลานจอดรถทดสอบ " + s.province,
			"fleet_size": intNum(3), "service_regions": s.regions, "vehicle_types": []any{"4 Wheels Jumbo", "6 Wheels"},
			"contractor_tenant_id": own, "status": "active", "created_at": ts(created)})
		tenants = append(tenants, g.tenant(id, s.code, false, admin))
		// Use the existing placeholder-object pipeline; no generator writes to storage itself.
		og := &opsGen{p: p, o: o}
		file, err := og.object("tenant_document", uuid.MustParse(id), "company_doc", created, id, admin)
		if err != nil {
			return err
		}
		g.add("tenant_files", Row{"tenant_id": id, "file_id": file, "kind": "company_doc", "position": intNum(0), "created_at": ts(created)})
	}
	for i, tenant := range tenants {
		keys := [3]string{}
		for j := range keys {
			n := i*3 + j
			keys[j] = fmt.Sprintf("fleet-demo:%s:%d", tenant.code, j)
			kind := truckModels[[]int{0, 2, 0}[j]]
			model := kind.models[0]
			s := truckSpec{key: keys[j], plate: fmt.Sprintf("9กข-%04d", 4101+n), province: []string{"กรุงเทพมหานคร", "ชลบุรี", "พระนครศรีอยุธยา"}[i],
				typeRaw: kind.typeRaw, brand: model[0], model: model[1], year: 2021 + j, color: "ขาว",
				pm: 10000, mileage: []int{130500, 89300, 63000}[j], status: "active",
				taxDue: day([]int{-14, 18, 180}[j]), insuranceDue: day([]int{12, 210, -7}[j]),
				insurer: insurers[i%len(insurers)], insuranceClass: "1"}
			if err := g.truck(tenant, s, created); err != nil {
				return err
			}
			truck := g.trucks[keys[j]]
			truck["registration_date"] = fmt.Sprintf("%d-11-20", s.year)
			truck["fuel_type"], truck["next_service_mileage"] = "Diesel", intNum([]int{130000, 90000, 70000}[j])
			truck["last_service_date"], truck["next_service_date"] = date(day([]int{-90, -45, -60}[j])), date(day([]int{-7, 12, 60}[j]))
			truck["tax_renewal_status"], truck["insurance_renewal_status"] = "pending", "pending"
			truck["created_by"], truck["notes"] = admin, "รถสาธิตสำหรับวางแผนต่อภาษี ประกันภัย และซ่อมบำรุง (ตัวอย่าง)"
			changed[truck.Str("id")] = fleetColumns
		}
		for j := range 2 {
			n := i*2 + j
			key := fmt.Sprintf("fleet-demo:%s:driver:%d", tenant.code, j)
			employment, hire := "subcontractor", ""
			if tenant.own {
				employment, hire = "full_time", date(created)
			}
			if err := g.driver(tenant, driverSpec{key: key, nid: thaiID(fmt.Sprintf("1999900041%02d", n+1)),
				mobile: fmt.Sprintf("000-000-04%02d", n+10), given: givenNames[8+n], family: familyNames[8+n],
				birth: fmt.Sprintf("%d-%02d-%02d", 1984+n, 2+n, 10+n*3), license: "ท.2", employment: employment, hire: hire, probation: true,
				status: "active", created: created}); err != nil {
				return err
			}
			if j == 0 {
				if err := g.assign(assignSpec{truck: keys[2], driver: key, status: "revoked", from: day(-60).Add(9 * time.Hour),
					to: day(-30).Add(17 * time.Hour), by: tenant}); err != nil {
					return err
				}
			}
			if err := g.assign(assignSpec{truck: keys[j], driver: key, status: "active", from: day(-29 + j*9).Add(9 * time.Hour), by: tenant}); err != nil {
				return err
			}
		}
		record := func(truck Row, typ, service, status string, start, booked time.Time) Row {
			r := Row{"id": g.id("maintenance_records", truck.Str("license_plate")+":"+date(start)+":"+typ),
				"tenant_id": tenant.id, "tenant_source": "truck", "truck_id": truck.Str("id"),
				"truck_license_plate_snapshot": truck.Str("license_plate"), "truck_brand_snapshot": truck.Str("brand"),
				"maintenance_type": typ, "service_type": service, "status": status, "start_date": date(start),
				"provider": "อู่ขนส่งทดสอบ (ตัวอย่าง)", "created_by": admin, "created_at": ts(booked)}
			g.add("maintenance_records", r)
			return r
		}
		completed := record(g.trucks[keys[1]], "PM", "เปลี่ยนถ่ายน้ำมันเครื่องและไส้กรอง", "completed", day(-45), day(-48).Add(10*time.Hour))
		completed["end_date"], completed["check_in_at"], completed["check_out_at"] = date(day(-45)), ts(day(-45).Add(9*time.Hour)), ts(day(-45).Add(15*time.Hour))
		completed["current_mileage"], completed["next_service_mileage"] = intNum(80000), intNum(90000)
		completed["cost_labor_thb"], completed["cost_parts_thb"], completed["total_cost_thb"] = "1200.00", "3650.00", "4850.00"
		completed["payment_method"], completed["updated_at"] = "transfer", completed["check_out_at"]
		for j, status := range []string{"pm_booking", "scheduled"} {
			truck := g.trucks[keys[j]]
			r := record(truck, "PM", "เช็คระยะและตรวจสภาพรถ", status, day(2+j*3), day(-2+j).Add(14*time.Hour))
			r["appointment_time"], r["current_mileage"], r["next_service_mileage"] = "09:30", truck["current_mileage"], intNum([]int{140000, 100000}[j])
			truck["active_maintenance_id"] = r.Str("id")
		}
		truck := g.trucks[keys[2]]
		cm := record(truck, "CM", "ซ่อมระบบเบรกหลัง", "in_progress", day(-2), day(-3).Add(16*time.Hour))
		cm["check_in_at"], cm["updated_at"], cm["notes"] = ts(day(-2).Add(8*time.Hour)), ts(day(-2).Add(8*time.Hour)), "รออะไหล่จานเบรกหลัง"
		truck["status"], truck["active_maintenance_id"] = "maintenance", cm.Str("id")
	}
	p.noteTruckChanges(changed)
	return nil
}

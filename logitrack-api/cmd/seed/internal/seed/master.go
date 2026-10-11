package seed

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// The master-data generators of the demo and load profiles (T26, issue #46; Appendix D §D.1.2): customers and
// billing parties, hubs and SOCs with their name aliases and Hub-SOC distances, trucks, drivers with their logins
// (users and driver memberships: every tenant gets driver logins beside the T16 staff personas; the roles each tenant
// holds are listed in Appendix D §D.1.2), and truck assignments with their history, the driver of an active one on
// duty. Smoke rows and the T16 identity rows are never changed: demo and load only append,
// so a database seeded with an older demo takes the new rows with --mode upsert. Every id is uuid v5 of the row's
// natural key (customer code, "customer:"/"tenant:" + code, hub code, "<hub>:<soc key>:<direction>", tenant code +
// plate, national ID, plate + national ID + Bangkok date) and every drawn value comes from the generator's own PCG
// stream (Appendix D §D.1.4), so two runs write the same rows (invariant 12).
//
// The rows keep the invariants of Appendix D §D.3: no alias or hub name is a hub code (#8, checked here before
// anything is written, as the hubs API does at write time); every truck, driver and assignment is stamped with one
// tenant and binds a driver of that tenant (#10a), except the R24 broker who moved tenant, whose old rows stay in the
// old tenant beside a suspended driver membership (#10e reports them as its own history).

// hubSpec is one generated hub or SOC.
type hubSpec struct {
	code, th, en string
	lat, lng     float64 // both 0: no coordinates (a driver-created hub)
	station      string  // HUB (default) or SOC
	party        string  // billing party id ("" = not linked)
	partyCode    string  // that party's customer code (hubs.NetworkGroup reads it)
	driver       bool    // created_by_driver (POST /v1/mobile/hubs: no coordinates, name_en = name_th)
	createdBy    string  // users.id of the creator ("" = NULL)
	legacy       string  // a legacy_hubName alias (the label the legacy screens stored, "SOCN (วังน้อย)")
	manual       string  // a manual alias
}

// demoHubs are the demo profile's hubs and SOCs on top of the nine of smoke: 15 hubs and the SOCs SOCN, SOCW and the
// stand-by point 0STANDBY, which the distance rule skips (Appendix D §D.1.2: 23 hubs + 4 SOCs). The codes reuse those
// of the web tests where one exists (BLANG-B, SOCN, SOCW in lib/placeFilter.test.ts; HUB-B in the billing golden
// vectors) so a fixture value means the same place here (Appendix D §D.1.9); names are district labels and the
// coordinates are synthetic. party holds a symbol here; demoMaster resolves it.
var demoHubs = []hubSpec{
	{code: "BLANG-B", th: "ห้วยขวาง", en: "Huai Khwang", lat: 13.7787, lng: 100.5862, party: "BP_SPX"},
	{code: "HUB-B", th: "บางกะปิ", en: "Bang Kapi", lat: 13.7659, lng: 100.6474, party: "BP_SPX"},
	{code: "LATPR-A", th: "ลาดพร้าว", en: "Lat Phrao", lat: 13.8160, lng: 100.5930, party: "BP_SPX"},
	{code: "BUENG-A", th: "บึงกุ่ม", en: "Bueng Kum", lat: 13.7853, lng: 100.6690, party: "BP_SPX"},
	{code: "MINBU-A", th: "มีนบุรี", en: "Min Buri", lat: 13.8134, lng: 100.7482, party: "BP_SPX"},
	{code: "SAPHN-A", th: "สะพานสูง", en: "Saphan Sung", lat: 13.7695, lng: 100.6843, party: "BP_SPX"},
	{code: "PRAKN-A", th: "พระโขนง", en: "Phra Khanong", lat: 13.7024, lng: 100.6010, party: "BP_SPX"},
	{code: "BKHAE-A", th: "บางแค", en: "Bang Khae", lat: 13.6961, lng: 100.4093, party: "BP_SPX"},
	{code: "DONMG-A", th: "ดอนเมือง", en: "Don Mueang", lat: 13.9131, lng: 100.5894, party: "BP_SPX"},
	{code: "NONTH-A", th: "นนทบุรี", en: "Nonthaburi", lat: 13.8591, lng: 100.5217, party: "BP_SPX"},
	{code: "CJSF-BPL", th: "คลังสินค้า CJSF บางพลี", en: "CJSF Bang Phli DC", lat: 13.6064, lng: 100.7062, party: "BP_CJSF"},
	{code: "CJSF-LKB", th: "คลังสินค้า CJSF ลาดกระบัง", en: "CJSF Lat Krabang DC", lat: 13.7271, lng: 100.7845, party: "BP_CJSF"},
	{code: "CJSF-SMP", th: "คลังสินค้า CJSF สมุทรปราการ", en: "CJSF Samut Prakan DC", lat: 13.5993, lng: 100.5968, party: "BP_CJSF"},
	{code: "TTP-HUB2", th: "ลานจอด TTP บางบัวทอง", en: "TTP Yard Bang Bua Thong", lat: 13.9100, lng: 100.4240, party: "BP_TTP"},
	{code: "RAMIN-DRV", th: "รามอินทรา (คนขับเพิ่ม)", driver: true},
	{code: "SOCN", th: "วังน้อย", en: "SOC North (Wang Noi)", lat: 14.2353, lng: 100.7197, station: hubs.StationSOC, party: "BP_SPX",
		legacy: "SOCN (วังน้อย)"},
	{code: "SOCW", th: "สมุทรสาคร", en: "SOC West (Samut Sakhon)", lat: 13.5475, lng: 100.2745, station: hubs.StationSOC, party: "BP_SPX",
		legacy: "SOCW (สมุทรสาคร)"},
	{code: "0STANDBY", th: "จุดจอดรอสแตนด์บาย", en: "Standby point", lat: 13.7440, lng: 100.6990, station: hubs.StationSOC},
}

// smokeSOCLabel is the legacy label of the smoke SOC SOCE (SOC_DESTINATIONS of validate/taskSchema.ts), a
// legacy_hubName alias the demo adds beside the SOCN and SOCW ones.
const smokeSOCLabel = "SOCE (บัวโรย)"

// district is a Bangkok district: the load profile's hub names and coordinates (synthetic, near its centre).
type district struct {
	th, en   string
	lat, lng float64
}

var districts = []district{
	{"พระนคร", "Phra Nakhon", 13.7563, 100.5018}, {"ดุสิต", "Dusit", 13.7769, 100.5207},
	{"หนองจอก", "Nong Chok", 13.8556, 100.8625}, {"บางรัก", "Bang Rak", 13.7308, 100.5243},
	{"บางเขน", "Bang Khen", 13.8732, 100.5963}, {"บางกะปิ", "Bang Kapi", 13.7659, 100.6474},
	{"ปทุมวัน", "Pathum Wan", 13.7445, 100.5223}, {"ป้อมปราบศัตรูพ่าย", "Pom Prap Sattru Phai", 13.7581, 100.5131},
	{"พระโขนง", "Phra Khanong", 13.7024, 100.6010}, {"มีนบุรี", "Min Buri", 13.8134, 100.7482},
	{"ลาดกระบัง", "Lat Krabang", 13.7226, 100.7593}, {"ยานนาวา", "Yan Nawa", 13.6966, 100.5434},
	{"สัมพันธวงศ์", "Samphanthawong", 13.7389, 100.5130}, {"พญาไท", "Phaya Thai", 13.7804, 100.5420},
	{"ธนบุรี", "Thon Buri", 13.7249, 100.4859}, {"บางกอกใหญ่", "Bangkok Yai", 13.7228, 100.4764},
	{"ห้วยขวาง", "Huai Khwang", 13.7764, 100.5795}, {"คลองสาน", "Khlong San", 13.7303, 100.5096},
	{"ตลิ่งชัน", "Taling Chan", 13.7769, 100.4566}, {"บางกอกน้อย", "Bangkok Noi", 13.7709, 100.4677},
	{"บางขุนเทียน", "Bang Khun Thian", 13.6602, 100.4359}, {"ภาษีเจริญ", "Phasi Charoen", 13.7149, 100.4374},
	{"หนองแขม", "Nong Khaem", 13.7044, 100.3496}, {"ราษฎร์บูรณะ", "Rat Burana", 13.6824, 100.5056},
	{"บางพลัด", "Bang Phlat", 13.7939, 100.5050}, {"ดินแดง", "Din Daeng", 13.7699, 100.5531},
	{"บึงกุ่ม", "Bueng Kum", 13.7853, 100.6690}, {"สาทร", "Sathon", 13.7081, 100.5262},
	{"บางซื่อ", "Bang Sue", 13.8094, 100.5371}, {"จตุจักร", "Chatuchak", 13.8285, 100.5597},
	{"บางคอแหลม", "Bang Kho Laem", 13.6934, 100.5025}, {"ประเวศ", "Prawet", 13.7165, 100.6942},
	{"คลองเตย", "Khlong Toei", 13.7081, 100.5838}, {"สวนหลวง", "Suan Luang", 13.7300, 100.6510},
	{"จอมทอง", "Chom Thong", 13.6777, 100.4842}, {"ดอนเมือง", "Don Mueang", 13.9131, 100.5894},
	{"ราชเทวี", "Ratchathewi", 13.7589, 100.5346}, {"ลาดพร้าว", "Lat Phrao", 13.8160, 100.5930},
	{"วัฒนา", "Watthana", 13.7422, 100.5856}, {"บางแค", "Bang Khae", 13.6961, 100.4093},
	{"หลักสี่", "Lak Si", 13.8875, 100.5789}, {"สายไหม", "Sai Mai", 13.9195, 100.6455},
	{"คันนายาว", "Khan Na Yao", 13.8269, 100.6794}, {"สะพานสูง", "Saphan Sung", 13.7695, 100.6843},
	{"วังทองหลาง", "Wang Thonglang", 13.7765, 100.6060}, {"คลองสามวา", "Khlong Sam Wa", 13.8597, 100.7044},
	{"บางนา", "Bang Na", 13.6681, 100.6044}, {"ทวีวัฒนา", "Thawi Watthana", 13.7724, 100.3536},
	{"ทุ่งครุ", "Thung Khru", 13.6467, 100.4996}, {"บางบอน", "Bang Bon", 13.6589, 100.3916},
}

// Names of generated drivers (synthetic: common given and family names combined arbitrarily, §D.1.9).
var (
	givenNames = [][2]string{{"ประสิทธิ์", "Prasit"}, {"สุชาติ", "Suchat"}, {"วีระ", "Weera"}, {"ชัยวัฒน์", "Chaiwat"},
		{"อนุชา", "Anucha"}, {"ทวีศักดิ์", "Thawisak"}, {"สมบัติ", "Sombat"}, {"นพดล", "Noppadon"}, {"สมศักดิ์", "Somsak"},
		{"ธนพล", "Thanaphon"}, {"พงศกร", "Phongsakon"}, {"ณัฐพล", "Natthaphon"}, {"เกรียงไกร", "Kriangkrai"},
		{"บุญเลิศ", "Boonlert"}, {"สายัณห์", "Sayan"}, {"จักรพันธ์", "Chakkraphan"}, {"ปิยะ", "Piya"}, {"สุริยา", "Suriya"},
		{"อภิชาติ", "Aphichat"}, {"วิทยา", "Witthaya"}}
	familyNames = [][2]string{{"แสงทอง", "Saengthong"}, {"มีสุข", "Meesuk"}, {"รักไทย", "Rakthai"}, {"สายใจ", "Saichai"},
		{"เพชรรัตน์", "Phetcharat"}, {"วงศ์สวัสดิ์", "Wongsawat"}, {"แก้วมณี", "Kaewmanee"}, {"ศรีวงศ์", "Sriwong"},
		{"ใจดี", "Jaidee"}, {"ทองดี", "Thongdee"}, {"ศรีสุข", "Srisuk"}, {"บุญมา", "Boonma"}, {"ทองคำ", "Thongkham"},
		{"ดวงดี", "Duangdee"}, {"พงษ์พันธ์", "Phongphan"}}
	provinces = []string{"กรุงเทพมหานคร", "สมุทรปราการ", "นนทบุรี", "ปทุมธานี", "สมุทรสาคร", "ชลบุรี", "พระนครศรีอยุธยา"}
	// plateConsonants are the Thai consonants plates use (ก-ฮ without the obsolete ฃ and ฅ).
	plateConsonants = []rune("กขคฆงจฉชซฌญฎฏฐฑฒณดตถทธนบปผฝพฟภมยรลวศษสหฬอฮ")
	insurers        = []string{"บริษัท ทดสอบประกันภัย จำกัด (ตัวอย่าง)", "บริษัท ตัวอย่างการประกัน จำกัด (ตัวอย่าง)"}
	insuranceTypes  = []string{"1", "2", "2+", "3", "3+"}
)

// truckModel is a type_raw (the truck form's full word, trucks.type_raw) with the brands and models it is drawn with.
type truckModel struct {
	typeRaw string
	weight  int // relative frequency in the load fleet
	models  [][2]string
}

var truckModels = []truckModel{
	{"4 Wheels Jumbo", 40, [][2]string{{"Isuzu", "D-Max Spark"}, {"Toyota", "Hilux Revo"}, {"Mitsubishi", "Triton"}}},
	{"4 Wheels", 10, [][2]string{{"Isuzu", "D-Max"}, {"Ford", "Ranger"}}},
	{"6 Wheels", 28, [][2]string{{"Hino", "300 XZU"}, {"Isuzu", "NMR"}, {"Fuso", "Canter"}}},
	{"10 Wheels", 12, [][2]string{{"Isuzu", "FVM"}, {"Hino", "500 FG"}}},
	{"18 Wheels", 3, [][2]string{{"Hino", "700"}, {"Volvo", "FH"}}},
	{"Pickup", 5, [][2]string{{"Toyota", "Hilux Revo"}, {"Nissan", "Navara"}}},
	{"Van", 2, [][2]string{{"Toyota", "Hiace"}}},
}

// tenantRef is a tenant the fleet generators write into, with the staff user its rows name.
type tenantRef struct {
	id, code   string
	own        bool   // the own fleet (ownership own, full-time drivers) or a carrier (subcontractor)
	admin      string // users.id that writes assignments and creates memberships
	adminLabel string // that user's display name (truck_assignments.admin_label)
}

// truckSpec is one generated truck.
type truckSpec struct {
	key                     string
	plate, province         string
	typeRaw, brand, model   string
	color                   string
	year, pm, mileage       int
	gps                     string
	status                  string
	taxDue, insuranceDue    time.Time
	insurer, insuranceClass string
}

// driverSpec is one generated driver; email "" leaves it without a login (a driver the office keeps without the app).
type driverSpec struct {
	key              string
	nid, mobile      string
	given, family    [2]string // Thai, romanized
	email            string
	birth            string
	license          string
	employment       string
	hire             string
	start, end       string
	probation        bool
	status           string
	created          time.Time
	former           *tenantRef // R24: the tenant it moved from, where its driver membership is suspended
	moved            time.Time
	formerMembership time.Time
}

// assignSpec is one truck assignment.
type assignSpec struct {
	truck, driver string // keys
	status        string // active | revoked | cancelled
	from, to      time.Time
	legacy        string // legacy_doc_id of a row the ETL would load (cancelled exists only there)
	by            *tenantRef
}

// masterGen holds what the generators share: the plan, the ids of the rows it wrote, and the business keys the
// database keeps unique (plates per tenant, hub codes, aliases), so a collision fails the build, never the load.
type masterGen struct {
	p       *Plan
	o       Options
	hash    any
	trucks  map[string]Row // key -> row
	plates  map[string]string
	drivers map[string]Row
	nids    map[string]bool
	codes   map[string]bool // upper hub codes
	names   map[string]string
	gps     map[string]bool
}

func newMasterGen(p *Plan, o Options) *masterGen {
	g := &masterGen{p: p, o: o, hash: passwordValue(p), trucks: map[string]Row{}, plates: map[string]string{},
		drivers: map[string]Row{}, nids: map[string]bool{}, codes: map[string]bool{}, names: map[string]string{}, gps: map[string]bool{}}
	for _, r := range p.Tables["trucks"] {
		g.plates[r.Str("license_plate")] = r.Str("tenant_id")
		if v := r.Str("gps_vehicle_id"); v != "" {
			g.gps[v] = true
		}
	}
	g.plates[orphanPlate] = ""
	for _, r := range p.Tables["drivers"] {
		g.nids[r.Str("id_card")] = true
	}
	for _, r := range p.Tables["hubs"] {
		g.codes[strings.ToUpper(r.Str("source_id"))] = true
	}
	for _, r := range p.Tables["hubs"] {
		for _, col := range []string{"name_th", "name_en"} {
			if n := strings.TrimSpace(r.Str(col)); n != "" {
				g.names[strings.ToLower(n)] = r.Str("source_id")
			}
		}
	}
	for _, r := range p.Tables["hub_name_aliases"] {
		g.names[strings.ToLower(r.Str("alias"))] = r.Str("hub_id")
	}
	return g
}

// orphanPlate deliberately has no trucks row (Appendix D §D.1.9).
const orphanPlate = "กข-1111"

func (g *masterGen) add(table string, r Row) { g.p.Tables[table] = append(g.p.Tables[table], r) }

func (g *masterGen) id(table, key string) string { return ID(g.o.Namespace, table, key).String() }

// userName is the display name of a seeded user ("" when it is not in the plan).
func (g *masterGen) userName(id string) string {
	for _, r := range g.p.Tables["users"] {
		if r.Str("id") == id {
			return r.Str("display_name")
		}
	}
	return ""
}

// tenant builds the reference of a seeded tenant whose staff user is admin.
func (g *masterGen) tenant(id, code string, own bool, admin string) *tenantRef {
	return &tenantRef{id: id, code: code, own: own, admin: admin, adminLabel: g.userName(admin)}
}

func num(f float64) json.Number { return json.Number(strconv.FormatFloat(f, 'f', -1, 64)) }

func intNum(n int) json.Number { return json.Number(strconv.Itoa(n)) }

func date(t time.Time) string { return clock.DateString(t) }

// lineGroupID is a well-formed LINE group id that resolves to no real group: "C" + md5 hex of "seed-line:{code}"
// (Appendix D §D.1.9).
func lineGroupID(code string) string {
	sum := md5.Sum([]byte("seed-line:" + code))
	return "C" + hex.EncodeToString(sum[:])
}

// legacyDocID is a synthetic Firestore-shaped doc id: 20 characters starting "seed" (Appendix D §D.1.3).
func legacyDocID(kind, suffix string) string {
	s := "seed" + kind
	return s + strings.Repeat("0", 20-len(s)-len(suffix)) + suffix
}

// --- hubs, aliases, distances ------------------------------------------------------------------------------

// hub appends one hub and its name -> code aliases (the hubs API's aliasNames: Thai and English names, trimmed,
// without one equal to the code, first writer wins; plus a legacy or manual alias). A code that exists, or a name or
// alias that is a hub code or another hub's name, is refused: invariant 8 holds before anything is written.
func (g *masterGen) hub(h hubSpec, created time.Time) (string, error) {
	code := hubs.NormalizeSourceID(h.code)
	if g.codes[strings.ToUpper(code)] {
		return "", fmt.Errorf("seed: hub %s exists in the plan", code)
	}
	if owner, taken := g.names[strings.ToLower(code)]; taken {
		return "", fmt.Errorf("seed: hub code %s is a name of %s (invariant 8)", code, owner)
	}
	if h.station == "" {
		h.station = hubs.StationHub
	}
	if h.driver && h.en == "" {
		h.en = h.th // POST /v1/mobile/hubs stores the driver's name in both columns
	}
	id := g.id("hubs", code)
	r := Row{"id": id, "source_id": code, "name_th": h.th, "station_type": h.station,
		"network": hubs.NetworkGroup(code, h.partyCode), "created_by_driver": h.driver, "created_at": ts(created)}
	if h.en != "" {
		r["name_en"] = h.en
	}
	if h.lat != 0 || h.lng != 0 {
		r["latitude"], r["longitude"] = num(h.lat), num(h.lng)
	}
	if h.party != "" {
		r["linked_party_id"] = h.party
	}
	if h.createdBy != "" {
		r["created_by"] = h.createdBy
	}
	g.codes[strings.ToUpper(code)] = true
	g.add("hubs", r)
	for _, a := range []struct{ name, source string }{{h.th, "name_th"}, {h.en, "name_en"}, {h.legacy, "legacy_hubName"}, {h.manual, "manual"}} {
		if err := g.alias(id, code, a.name, a.source, created); err != nil {
			return "", err
		}
	}
	return id, nil
}

// alias appends one name -> code alias; a blank name or the hub's own code is skipped, as aliasNames does.
func (g *masterGen) alias(hubID, code, name, source string, created time.Time) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, code) {
		return nil
	}
	key := strings.ToLower(name)
	if owner, taken := g.names[key]; taken {
		if owner == hubID || owner == code {
			return nil // the hub's own Thai and English names are one alias
		}
		return fmt.Errorf("seed: hub name %q of %s is already a name of %s", name, code, owner)
	}
	if g.codes[strings.ToUpper(name)] {
		return fmt.Errorf("seed: hub name %q of %s is a hub code (invariant 8)", name, code)
	}
	g.names[key] = hubID
	g.add("hub_name_aliases", Row{"alias": name, "hub_id": hubID, "source": source, "created_at": ts(created)})
	return nil
}

// distancePoint is a hub or SOC of the distance rule.
type distancePoint struct {
	id, code, key, network string
	lat, lng               float64
}

// distances appends the Hub-SOC pairs the distances.compute job would write for every hub in the plan that does not
// have them yet: the job's point rule (internal/hubs points: HUBs; SOCs except the stand-by codes starting with 0, the
// first per network and key), pairs only inside one network, both directions (R14). The Distance Matrix is not
// called: the distance is synthetic, haversine x 1.3 at 35 km/h (Appendix D §D.1.2), rounded as the job rounds the
// API's answer (whole metres and seconds; km and minutes Math.round(x * 100) / 100).
func (g *masterGen) distances(created time.Time, by string) {
	var hubPts, socPts []distancePoint
	seen := map[string]bool{}
	for _, r := range g.p.Tables["hubs"] {
		lat, ok1 := floatOf(r["latitude"])
		lng, ok2 := floatOf(r["longitude"])
		if !ok1 || !ok2 {
			continue
		}
		pt := distancePoint{id: r.Str("id"), code: r.Str("source_id"), network: r.Str("network"), lat: lat, lng: lng}
		if pt.network != hubs.NetworkSPK {
			pt.network = hubs.NetworkSPX
		}
		if hubs.NormalizeStationType(r.Str("station_type")) == hubs.StationHub {
			hubPts = append(hubPts, pt)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(pt.code), "0") {
			continue // stand-by points, never computed
		}
		pt.key = hubs.NormalizeSocKey(pt.code)
		if seen[pt.network+"_"+pt.key] {
			continue
		}
		seen[pt.network+"_"+pt.key] = true
		socPts = append(socPts, pt)
	}
	have := map[string]bool{}
	for _, r := range g.p.Tables["hub_soc_distances"] {
		have[r.Str("hub_id")+"|"+r.Str("soc_key")+"|"+r.Str("direction")] = true
	}
	for _, h := range hubPts {
		for _, s := range socPts {
			if s.network != h.network {
				continue
			}
			meters, seconds := syntheticRoute(h.lat, h.lng, s.lat, s.lng)
			for _, dir := range []string{hubs.DirectionHubToSOC, hubs.DirectionSOCToHub} {
				if have[h.id+"|"+s.key+"|"+dir] {
					continue
				}
				row := Row{"id": g.id("hub_soc_distances", h.code+":"+s.key+":"+dir), "hub_id": h.id, "soc_key": s.key,
					"direction": dir, "network": h.network, "created_by": by, "created_at": ts(created)}
				row["distance_m"], row["duration_s"] = intNum(meters), intNum(seconds)
				row["distance_km"] = fmt.Sprintf("%.2f", jsmath.Round2(float64(meters)/1000))
				row["duration_min"] = fmt.Sprintf("%.2f", jsmath.Round2(float64(seconds)/60))
				row["hub_lat"], row["hub_lng"], row["soc_lat"], row["soc_lng"] = num(h.lat), num(h.lng), num(s.lat), num(s.lng)
				g.add("hub_soc_distances", row)
			}
		}
	}
}

// Synthetic routing of the seed (no Distance Matrix call): the great-circle distance times a road factor, driven at
// a city truck speed. The smoke pair ALANG-A <-> SOCE (29,405 m, 3,024 s) is this formula's output.
const (
	earthRadiusM   = 6371000.0
	roadFactor     = 1.3
	truckSpeedKmph = 35.0
)

// syntheticRoute is the road distance in whole metres and the driving time in whole seconds of one pair.
func syntheticRoute(lat1, lng1, lat2, lng2 float64) (meters, seconds int) {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dp, dl := rad(lat2-lat1), rad(lng2-lng1)
	sp, sl := math.Sin(dp/2), math.Sin(dl/2)
	// The products of the sum are converted explicitly so no platform fuses them into a multiply-add (the jsmath
	// rule): the same coordinates give the same metres on amd64 and arm64.
	h := float64(sp*sp) + float64(float64(math.Cos(rad(lat1))*math.Cos(rad(lat2)))*float64(sl*sl))
	road := 2 * earthRadiusM * math.Asin(math.Sqrt(h)) * roadFactor
	return int(math.Round(road)), int(math.Round(road / (truckSpeedKmph * 1000 / 3600)))
}

// floatOf reads a coordinate of a plan row (json.Number in fixture and generated rows).
func floatOf(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	}
	return 0, false
}

// --- trucks, drivers, assignments --------------------------------------------------------------------------

// truck appends one truck of t: the vehicle class is internal/fleet/vehicleclass.Of(type_raw), as every Go writer
// derives it (R15); a plate already used in the plan, or the orphan plate, is refused.
func (g *masterGen) truck(t *tenantRef, s truckSpec, created time.Time) error {
	if _, taken := g.plates[s.plate]; taken {
		return fmt.Errorf("seed: plate %s is taken (or is the orphan plate)", s.plate)
	}
	g.plates[s.plate] = t.id
	ownership := "subcontractor"
	if t.own {
		ownership = "own"
	}
	r := Row{"id": g.id("trucks", t.code+":"+s.plate), "tenant_id": t.id, "tenant_source": "self", "ownership_type": ownership,
		"license_plate": s.plate, "province": s.province, "status": s.status, "brand": s.brand, "model": s.model,
		"year": intNum(s.year), "color": s.color, "type_raw": s.typeRaw, "created_at": ts(created)}
	if c := vehicleclass.Of(s.typeRaw); c != nil {
		r["vehicle_class"] = *c
	}
	if s.gps != "" {
		if g.gps[s.gps] {
			return fmt.Errorf("seed: GPS vehicle id %s generated twice", s.gps)
		}
		g.gps[s.gps] = true
		r["gps_vehicle_id"] = s.gps
	}
	if s.pm > 0 {
		r["pm_interval_km"], r["current_mileage"] = intNum(s.pm), intNum(s.mileage)
		r["next_service_mileage"] = intNum((s.mileage/s.pm + 1) * s.pm)
	}
	if !s.taxDue.IsZero() {
		r["tax_expiry_date"], r["tax_expense_thb"] = date(s.taxDue), "1950.00"
	}
	if !s.insuranceDue.IsZero() {
		r["insurance_company"], r["insurance_type"] = s.insurer, s.insuranceClass
		r["insurance_policy_number"] = "POL-SEED-" + strings.ToUpper(s.plate[len(s.plate)-4:]) + "-" + t.code
		r["insurance_start_date"], r["insurance_expiry_date"] = date(s.insuranceDue.AddDate(-1, 0, 0)), date(s.insuranceDue)
		r["insurance_premium_thb"] = map[string]string{"1": "21500.00", "2": "12800.00", "2+": "9800.00", "3": "4200.00", "3+": "7500.00"}[s.insuranceClass]
	}
	g.trucks[s.key] = r
	g.add("trucks", r)
	return nil
}

// driver appends one driver of t; a driver with an e-mail gets its login: a users row with SEED_DEFAULT_PASSWORD and
// an active driver membership in t (t_driver_link_membership, Appendix C §C.3.6). The R24 broker (former set) also
// keeps its suspended driver membership in the tenant it left.
func (g *masterGen) driver(t *tenantRef, s driverSpec) error {
	if g.nids[s.nid] {
		return fmt.Errorf("seed: national ID %s generated twice", s.nid)
	}
	g.nids[s.nid] = true
	th := s.given[0] + " " + s.family[0]
	r := Row{"id": g.id("drivers", s.nid), "tenant_id": t.id, "tenant_source": "self", "first_name": s.given[1],
		"last_name": s.family[1], "full_name_th": th, "mobile": s.mobile, "birth_date": s.birth, "id_card": s.nid,
		"license_type": s.license, "employment_type": s.employment, "probation_passed": s.probation, "status": s.status,
		"created_at": ts(s.created)}
	for col, v := range map[string]string{"hire_date": s.hire, "employment_start": s.start, "employment_end": s.end} {
		if v != "" {
			r[col] = v
		}
	}
	if s.email != "" {
		uid := g.id("users", s.email)
		r["user_id"] = uid
		g.add("users", Row{"id": uid, "email": s.email, "email_verified": true, "display_name": th, "password_hash": g.hash,
			"status": "active", "auth_version": json.Number("1"), "created_at": ts(s.created)})
		if s.former != nil {
			g.add("memberships", Row{"user_id": uid, "tenant_id": s.former.id, "role": "driver", "status": "suspended",
				"created_by": s.former.admin, "created_at": ts(s.formerMembership), "updated_at": ts(s.moved)})
		}
		joined := s.created
		if s.former != nil {
			joined = s.moved
		}
		g.add("memberships", Row{"user_id": uid, "tenant_id": t.id, "role": "driver", "status": "active",
			"created_by": t.admin, "created_at": ts(joined)})
	}
	if s.former != nil {
		r["updated_at"] = ts(s.moved)
	}
	g.drivers[s.key] = r
	g.add("drivers", r)
	return nil
}

// assign appends one truck assignment, stamped with the tenant of its truck (tenant_source 'truck', as POST
// /v1/truck-assignments writes it); an active one becomes the driver's home truck (drivers.current_assignment_id,
// back-filled after the load, §D.1.5) and puts an active driver on duty, as CreateAssignment does (internal/fleet:
// on_duty while the driver holds a truck), so a revoke of a seeded assignment releases the driver as it releases one
// the API assigned. The seed writes no status_history rows (that table is not seeded).
func (g *masterGen) assign(a assignSpec) error {
	tr, dr := g.trucks[a.truck], g.drivers[a.driver]
	if tr == nil || dr == nil {
		return fmt.Errorf("seed: assignment %s/%s names an unknown truck or driver", a.truck, a.driver)
	}
	id := g.id("truck_assignments", tr.Str("license_plate")+"|"+dr.Str("id_card")+"|"+date(a.from))
	r := Row{"id": id, "tenant_id": tr.Str("tenant_id"), "tenant_source": "truck", "truck_id": tr.Str("id"),
		"driver_id": dr.Str("id"), "status": a.status, "created_at": ts(a.from)}
	if a.legacy != "" || a.by == nil {
		r["admin_label"] = "System"
	} else {
		r["admin_user_id"], r["admin_label"] = a.by.admin, a.by.adminLabel
	}
	if a.legacy != "" {
		r["legacy_doc_id"] = a.legacy
	}
	if a.status == "revoked" {
		r["revoked_at"], r["updated_at"] = ts(a.to), ts(a.to)
	}
	if a.status == "active" {
		if dr["current_assignment_id"] != nil {
			return fmt.Errorf("seed: driver %s has two active assignments", a.driver)
		}
		dr["current_assignment_id"] = id
		if dr.Str("status") == "active" {
			dr["status"] = "on_duty"
		}
	}
	g.add("truck_assignments", r)
	return nil
}

// --- demo --------------------------------------------------------------------------------------------------

// addDemoMaster appends the demo master data (Appendix D §D.1.2): the TTP tenant party, 18 hubs and SOCs with 38
// aliases and the SPX Hub-SOC pairs, and a fleet per tenant (6 trucks, 8 drivers of which 6 sign in, 11 assignments in
// every status) that includes a second R24 broker: d13 moved from TTP to NWR, its suspended TTP membership and revoked
// TTP assignment beside its active NWR ones.
func addDemoMaster(p *Plan, o Options) error {
	g := newMasterGen(p, o)
	s := p.Symbols
	base := o.Anchor.AddDate(0, 0, -90).Add(9 * time.Hour)
	day := func(n int) time.Time { return base.AddDate(0, 0, n) }

	g.add("billing_parties", Row{"id": g.id("billing_parties", "tenant:TTP"), "kind": "tenant",
		"tenant_id": s.MustID("TN_TTP").String(), "billing_date_basis": "delivered", "created_at": ts(base)})

	codes := map[string]string{"BP_SPX": "SPX", "BP_CJSF": "CJSF", "BP_TTP": "TTP"}
	for i, h := range demoHubs {
		if h.party != "" {
			h.partyCode, h.party = codes[h.party], s.MustID(h.party).String()
		}
		created := base.Add(time.Duration(i) * time.Minute)
		if h.driver { // added from the app by d8 once it drove
			h.createdBy, created = g.id("users", demoDrivers[0].email), day(20)
		}
		if _, err := g.hub(h, created); err != nil {
			return err
		}
	}
	if err := g.alias(s.MustID("H_SOCE").String(), "SOCE", smokeSOCLabel, "legacy_hubName", base); err != nil {
		return err
	}
	g.distances(o.Anchor.AddDate(0, 0, -30).Add(3*time.Hour), s.MustID("U_WRT_OPS").String())

	own := g.tenant(s.MustID("TN_OWN").String(), "WRT", true, s.MustID("U_WRT_OPS").String())
	nwr := g.tenant(s.MustID("TN_NWR").String(), "NWR", false, s.MustID("U_NWR_ADMIN").String())
	ttp := g.tenant(s.MustID("TN_TTP").String(), "TTP", false, g.id("users", "ttp.admin@logitrack.test"))
	tenantOf := map[string]*tenantRef{"WRT": own, "NWR": nwr, "TTP": ttp}
	g.trucks["T3"] = rowByID(p, "trucks", s.MustID("T3").String()) // the smoke 6WH nobody drives yet
	for _, t := range demoTrucks {
		t.taxDue, t.insuranceDue = o.Anchor.AddDate(0, 0, t.mileage%97-20), o.Anchor.AddDate(0, 0, t.mileage%211+5)
		t.insurer, t.insuranceClass = insurers[t.year%2], insuranceTypes[t.year%len(insuranceTypes)]
		if err := g.truck(tenantOf[t.key[:3]], t, base); err != nil {
			return err
		}
	}
	for _, d := range demoDrivers {
		spec := d.driverSpec
		spec.created = day(d.createdDay)
		if spec.employment == "part_time" { // a contract around the anchor, whatever SEED_ANCHOR_DATE is
			spec.start, spec.end = date(spec.created), date(o.Anchor.AddDate(0, 3, 0))
		}
		if d.from != "" {
			spec.former, spec.formerMembership, spec.moved = tenantOf[d.from], spec.created, day(d.movedDay)
		}
		if err := g.driver(tenantOf[d.tenant], spec); err != nil {
			return err
		}
	}
	for _, a := range demoAssignments {
		spec := assignSpec{truck: a.truck, driver: a.driver, status: a.status, from: day(a.from), to: day(a.to), legacy: a.legacy}
		if a.legacy == "" {
			spec.by = tenantOf[a.by]
		}
		if err := g.assign(spec); err != nil {
			return err
		}
	}
	return nil
}

// rowByID is a plan row by id (nil when absent).
func rowByID(p *Plan, table, id string) Row {
	for _, r := range p.Tables[table] {
		if r.Str("id") == id {
			return r
		}
	}
	return nil
}

// demoTrucks are the demo trucks: the key's first three letters name the tenant (the smoke T3 joins them as the
// truck of d10).
var demoTrucks = []truckSpec{
	{key: "WRT1", plate: "6กท-2045", province: "กรุงเทพมหานคร", typeRaw: "4 Wheels Jumbo", brand: "Isuzu", model: "D-Max Spark",
		color: "ขาว", year: 2023, gps: "CT-SEED-0101", pm: 10000, mileage: 45210, status: "active"},
	{key: "WRT2", plate: "7กษ-3187", province: "สมุทรปราการ", typeRaw: "6 Wheels", brand: "Hino", model: "300 XZU",
		color: "ขาว", year: 2022, gps: "CT-SEED-0102", pm: 15000, mileage: 132880, status: "active"},
	{key: "WRT3", plate: "8กร-5520", province: "กรุงเทพมหานคร", typeRaw: "10 Wheels", brand: "Isuzu", model: "FVM",
		color: "น้ำเงิน", year: 2021, gps: "CT-SEED-0103", pm: 20000, mileage: 241500, status: "active"},
	{key: "NWR1", plate: "2กล-6603", province: "นนทบุรี", typeRaw: "4 Wheels Jumbo", brand: "Toyota", model: "Hilux Revo",
		color: "เทา", year: 2021, pm: 10000, mileage: 98450, status: "active"},
	{key: "NWR2", plate: "3กว-7714", province: "ปทุมธานี", typeRaw: "4 Wheels", brand: "Isuzu", model: "D-Max",
		color: "ขาว", year: 2020, pm: 10000, mileage: 156020, status: "active"},
	{key: "TTP1", plate: "4กพ-8825", province: "กรุงเทพมหานคร", typeRaw: "6 Wheels", brand: "Hino", model: "300 XZU",
		color: "ขาว", year: 2023, pm: 15000, mileage: 38900, status: "active"},
}

// demoDriver is a demo driver: its tenant code, the day (after the demo base) it was created, and for the R24 broker
// the tenant it left and the day it moved.
type demoDriver struct {
	driverSpec
	tenant, from         string
	createdDay, movedDay int
}

var demoDrivers = []demoDriver{
	{driverSpec: driverSpec{key: "D8", nid: thaiID("199990000008"), mobile: "000-000-0108", given: givenNames[0], family: familyNames[0],
		email: "d8.prasit@logitrack.test", birth: "1987-04-21", license: "ท.2", employment: "full_time", hire: "2025-02-01",
		probation: true, status: "active"}, tenant: "WRT", createdDay: 1},
	{driverSpec: driverSpec{key: "D9", nid: thaiID("199990000009"), mobile: "000-000-0109", given: givenNames[1], family: familyNames[1],
		email: "d9.suchat@logitrack.test", birth: "1983-09-12", license: "ท.3", employment: "full_time", hire: "2024-11-15",
		probation: true, status: "active"}, tenant: "WRT", createdDay: 1},
	{driverSpec: driverSpec{key: "D10", nid: thaiID("199990000010"), mobile: "000-000-0110", given: givenNames[2], family: familyNames[2],
		email: "d10.weera@logitrack.test", birth: "1996-01-30", license: "ท.2", employment: "part_time", status: "active"},
		tenant: "WRT", createdDay: 10},
	{driverSpec: driverSpec{key: "D11", nid: thaiID("199990000011"), mobile: "000-000-0111", given: givenNames[3], family: familyNames[3],
		birth: "1979-06-05", license: "ท.3", employment: "full_time", hire: "2023-05-01", probation: true, status: "active"},
		tenant: "WRT", createdDay: 0},
	{driverSpec: driverSpec{key: "D12", nid: thaiID("199990000012"), mobile: "000-000-0112", given: givenNames[4], family: familyNames[4],
		email: "d12.anucha@logitrack.test", birth: "1991-08-18", license: "ท.2", employment: "subcontractor", status: "active"},
		tenant: "NWR", createdDay: 5},
	{driverSpec: driverSpec{key: "D13", nid: thaiID("199990000013"), mobile: "000-000-0113", given: givenNames[5], family: familyNames[5],
		email: "d13.thawisak@logitrack.test", birth: "1985-02-27", license: "ท.2", employment: "subcontractor", status: "active"},
		tenant: "NWR", from: "TTP", createdDay: 2, movedDay: 45},
	{driverSpec: driverSpec{key: "D14", nid: thaiID("199990000014"), mobile: "000-000-0114", given: givenNames[6], family: familyNames[6],
		email: "d14.sombat@logitrack.test", birth: "1989-11-09", license: "ท.2", employment: "subcontractor", status: "active"},
		tenant: "TTP", createdDay: 46},
	{driverSpec: driverSpec{key: "D15", nid: thaiID("199990000015"), mobile: "000-000-0115", given: givenNames[7], family: familyNames[7],
		birth: "1975-03-14", license: "ท.2", employment: "subcontractor", status: "inactive"}, tenant: "TTP", createdDay: 3},
}

// demoAssignment is a demo assignment; days count from the demo base, by names the tenant whose staff wrote it.
type demoAssignment struct {
	truck, driver, status string
	from, to              int
	by, legacy            string
}

var demoAssignments = []demoAssignment{
	{truck: "WRT1", driver: "D8", status: "active", from: 1, by: "WRT"},
	{truck: "WRT3", driver: "D9", status: "revoked", from: 1, to: 30, by: "WRT"},
	{truck: "WRT2", driver: "D9", status: "active", from: 30, by: "WRT"},
	{truck: "WRT3", driver: "D11", status: "active", from: 31, by: "WRT"},
	{truck: "T3", driver: "D10", status: "active", from: 10, by: "WRT"},
	{truck: "WRT2", driver: "D11", status: "cancelled", from: 0, legacy: legacyDocID("AsgDoc", "D11C")},
	{truck: "NWR2", driver: "D12", status: "revoked", from: 5, to: 44, by: "NWR"},
	{truck: "NWR1", driver: "D12", status: "active", from: 44, by: "NWR"},
	{truck: "TTP1", driver: "D13", status: "revoked", from: 2, to: 45, by: "TTP"},
	{truck: "NWR2", driver: "D13", status: "active", from: 45, by: "NWR"},
	{truck: "TTP1", driver: "D14", status: "active", from: 46, by: "TTP"},
}

// --- load --------------------------------------------------------------------------------------------------

// Load-profile master scale (Appendix D §D.1.2): customers LC01-LC08 with a party each and a tenant party per load
// carrier; 377 hubs (every eighth a J&T SPK hub, a few created by drivers); a fleet per tenant.
const (
	loadCustomers      = 8
	loadHubs           = 377
	loadOwnTrucks      = 44
	loadOwnDrivers     = 45
	loadOwnRevoked     = 11
	loadCarrierTrucks  = 28
	loadCarrierDrivers = 30
	loadCarrierRevoked = 7
)

// addLoadMaster appends the load master data on top of demo.
func addLoadMaster(p *Plan, o Options) error {
	g := newMasterGen(p, o)
	s := p.Symbols
	created := o.Anchor.AddDate(-1, 0, 0).Add(9 * time.Hour)
	parties := []hubSpec{{party: s.MustID("BP_SPX").String(), partyCode: "SPX"}, {party: s.MustID("BP_CJSF").String(), partyCode: "CJSF"},
		{party: s.MustID("BP_TTP").String(), partyCode: "TTP"}}
	for c := 1; c <= loadCustomers; c++ {
		code := fmt.Sprintf("LC%02d", c)
		terms, basis := 30, "delivered"
		if c%3 == 0 {
			terms = 15
		}
		if c%4 == 0 {
			basis = "plan"
		}
		cid := g.id("customers", code)
		g.add("customers", Row{"id": cid, "code": code, "name": "ลูกค้าทดสอบ " + code + " (ตัวอย่าง)",
			"tax_id": thaiID(fmt.Sprintf("0999900002%02d", c)), "branch_type": "hq", "payment_terms_days": intNum(terms),
			"billing_email": "billing." + strings.ToLower(code) + "@logitrack.test", "line_group_id": lineGroupID(code),
			"created_at": ts(created)})
		pid := g.id("billing_parties", "customer:"+code)
		g.add("billing_parties", Row{"id": pid, "kind": "customer", "customer_id": cid, "billing_date_basis": basis, "created_at": ts(created)})
		parties = append(parties, hubSpec{party: pid, partyCode: code})
	}
	carriers := make([]*tenantRef, 0, loadCarriers)
	for c := 1; c <= loadCarriers; c++ {
		code := fmt.Sprintf("L%02d", c)
		tid := g.id("tenants", code)
		g.add("billing_parties", Row{"id": g.id("billing_parties", "tenant:"+code), "kind": "tenant", "tenant_id": tid,
			"billing_date_basis": "delivered", "created_at": ts(created)})
		carriers = append(carriers, g.tenant(tid, code, false, g.id("users", strings.ToLower(code)+".admin@logitrack.test")))
	}

	r := rng(o, "load-master-hubs")
	spk := s.MustID("BP_SPK").String()
	for i := range loadHubs {
		d := districts[i%len(districts)]
		seq := i/len(districts) + 1
		h := hubSpec{code: fmt.Sprintf("LD%03d-A", i+1), th: fmt.Sprintf("%s-%02d", d.th, seq), en: fmt.Sprintf("%s %02d", d.en, seq),
			lat: round4(d.lat + float64((r.Float64()-0.5)*0.04)), lng: round4(d.lng + float64((r.Float64()-0.5)*0.04))}
		switch {
		case i%8 == 7:
			h.code, h.party, h.partyCode = fmt.Sprintf("SPK89%04d", 1001+i), spk, "SPK"
		case i%40 == 19:
			h.code, h.en, h.lat, h.lng, h.driver = fmt.Sprintf("LD%03d-DRV", i+1), "", 0, 0, true
		default:
			pick := parties[weighted(r, len(parties))]
			h.party, h.partyCode = pick.party, pick.partyCode
		}
		if i%25 == 0 {
			h.manual = h.th + " (เดิม)"
		}
		if _, err := g.hub(h, created.Add(time.Duration(i)*time.Minute)); err != nil {
			return err
		}
	}
	g.distances(o.Anchor.AddDate(0, 0, -30).Add(3*time.Hour), s.MustID("U_WRT_OPS").String())

	own := g.tenant(s.MustID("TN_OWN").String(), "WRT", true, s.MustID("U_WRT_OPS").String())
	nid := 1000
	if err := g.loadFleet(own, loadOwnTrucks, loadOwnDrivers, loadOwnRevoked, created, &nid); err != nil {
		return err
	}
	for _, c := range carriers {
		if err := g.loadFleet(c, loadCarrierTrucks, loadCarrierDrivers, loadCarrierRevoked, created, &nid); err != nil {
			return err
		}
	}
	return nil
}

// weighted picks a party index: SPX four times as often as each other party.
func weighted(r *rand.Rand, n int) int {
	if k := r.IntN(n + 3); k < n {
		return k
	}
	return 0
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// loadFleet appends nTrucks trucks and nDrivers drivers of t on t's own PCG stream: driver i < nTrucks drives truck i
// (active), the others are spare (the last one inactive); revoked earlier assignments of trucks to drivers make the
// history, each of a different truck and a different driver, so no driver or truck holds two assignments at once
// (the service answers 409 driver_assigned / truck_assigned), and all of them end before the active ones start.
// Nine drivers in ten sign in (a users row and a driver membership); the tenth is kept without the app.
func (g *masterGen) loadFleet(t *tenantRef, nTrucks, nDrivers, revoked int, created time.Time, nid *int) error {
	r := rng(g.o, "load-master-fleet:"+t.code)
	anchor := g.o.Anchor
	total := 0
	for _, m := range truckModels {
		total += m.weight
	}
	for i := range nTrucks {
		pick, k := r.IntN(total), 0
		for pick >= truckModels[k].weight {
			pick -= truckModels[k].weight
			k++
		}
		m := truckModels[k]
		bm := m.models[r.IntN(len(m.models))]
		spec := truckSpec{key: fmt.Sprintf("%s-T%03d", t.code, i), plate: g.drawPlate(r), province: provinces[r.IntN(len(provinces))],
			typeRaw: m.typeRaw, brand: bm[0], model: bm[1], color: []string{"ขาว", "เทา", "น้ำเงิน", "ดำ"}[r.IntN(4)],
			year: 2015 + r.IntN(11), pm: []int{10000, 15000, 20000}[r.IntN(3)], mileage: 20000 + r.IntN(280000), status: "active",
			taxDue: anchor.AddDate(0, 0, r.IntN(360)-30), insuranceDue: anchor.AddDate(0, 0, r.IntN(360)-20),
			insurer: insurers[r.IntN(len(insurers))], insuranceClass: insuranceTypes[r.IntN(len(insuranceTypes))]}
		if t.own {
			spec.gps = fmt.Sprintf("CT-SEED-%04d", 1001+i)
		}
		if i%13 == 12 {
			spec.status = "maintenance"
		}
		if err := g.truck(t, spec, created); err != nil {
			return err
		}
	}
	for i := range nDrivers {
		*nid++
		given, family := givenNames[r.IntN(len(givenNames))], familyNames[r.IntN(len(familyNames))]
		born := time.Date(1965+r.IntN(36), time.Month(1+r.IntN(12)), 1+r.IntN(28), 0, 0, 0, 0, clock.Bangkok)
		spec := driverSpec{key: fmt.Sprintf("%s-D%03d", t.code, i), nid: thaiID(fmt.Sprintf("19999%07d", *nid)),
			mobile: fmt.Sprintf("000-000-%04d", 600+*nid-1001), given: given, family: family, birth: date(born),
			license: "ท.2", employment: "subcontractor", status: "active", created: created.Add(time.Duration(i) * time.Minute)}
		if i%10 != 9 {
			spec.email = fmt.Sprintf("%s.d%03d@logitrack.test", strings.ToLower(t.code), i+1)
		}
		if i < nTrucks {
			switch g.trucks[fmt.Sprintf("%s-T%03d", t.code, i)].Str("vehicle_class") {
			case "10WH":
				spec.license = "ท.3"
			case "18WH":
				spec.license = "ท.4"
			}
		}
		if t.own {
			hired := anchor.AddDate(0, 0, -(60 + r.IntN(2400)))
			spec.employment, spec.hire, spec.probation = "full_time", date(hired), hired.Before(anchor.AddDate(0, 0, -119))
			if i%11 == 5 {
				spec.employment, spec.hire, spec.probation = "part_time", "", false
				spec.start, spec.end = date(anchor.AddDate(0, -3, 0)), date(anchor.AddDate(0, 3, 0))
			}
		}
		if i == nDrivers-1 && nDrivers > nTrucks {
			spec.status = "inactive"
		}
		if err := g.driver(t, spec); err != nil {
			return err
		}
	}
	// One driver per revoked row: a permutation, so two rows whose spans overlap never share a driver; the trucks
	// (j*5) % nTrucks are distinct for j < revoked as well.
	if revoked > nDrivers || revoked > nTrucks {
		return fmt.Errorf("seed: %s: %d revoked assignments need as many drivers and trucks", t.code, revoked)
	}
	perm := r.Perm(nDrivers)
	for j := range revoked {
		from := created.AddDate(0, 0, 7*j+1)
		if err := g.assign(assignSpec{truck: fmt.Sprintf("%s-T%03d", t.code, (j*5)%nTrucks),
			driver: fmt.Sprintf("%s-D%03d", t.code, perm[j]), status: "revoked", from: from,
			to: from.AddDate(0, 0, 3+r.IntN(20)), by: t}); err != nil {
			return err
		}
	}
	for i := range min(nTrucks, nDrivers) {
		if err := g.assign(assignSpec{truck: fmt.Sprintf("%s-T%03d", t.code, i), driver: fmt.Sprintf("%s-D%03d", t.code, i),
			status: "active", from: created.AddDate(0, 0, 7*revoked+30+i%60), by: t}); err != nil {
			return err
		}
	}
	return nil
}

// drawPlate draws a plate the truck form accepts ("1กข-2345": a digit, two consonants, a dash, four digits) that no
// truck of the plan carries yet.
func (g *masterGen) drawPlate(r *rand.Rand) string {
	for {
		p := fmt.Sprintf("%d%c%c-%04d", 1+r.IntN(9), plateConsonants[r.IntN(len(plateConsonants))],
			plateConsonants[r.IntN(len(plateConsonants))], 1000+r.IntN(9000))
		if _, taken := g.plates[p]; !taken {
			return p
		}
	}
}

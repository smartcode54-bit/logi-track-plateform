package bangchak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A response of today's format: an array whose first element carries the price list as a JSON string (OilList).
const oilListBody = `[{"OilList":"[{\"OilName\":\"ไฮดีเซล S\",\"PriceToday\":\"32.94\",\"Unit\":\"บาท/ลิตร\"},` +
	`{\"OilName\":\"แก๊สโซฮอล์ 95 S EVO\",\"PriceToday\":35.05},` +
	`{\"OilName\":\"ไม่มีราคา\"},` +
	`{\"OilName\":\"ราคาว่าง\",\"PriceToday\":\"\",\"Unit\":null},` +
	`{\"PriceToday\":\"40\"}]","OilDateNow":"11/10/2569"}]`

func TestParseOilList(t *testing.T) {
	got, err := Parse([]byte(oilListBody))
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{NameTh: "ไฮดีเซล S", NameEn: "ไฮดีเซล S", Price: 32.94, Unit: "บาท/ลิตร"},
		{NameTh: "แก๊สโซฮอล์ 95 S EVO", NameEn: "แก๊สโซฮอล์ 95 S EVO", Price: 35.05, Unit: DefaultUnit},
		// PriceToday missing: Number(undefined) is NaN, the item is dropped; "" is Number("") = 0, kept as 0.
		{NameTh: "ราคาว่าง", NameEn: "ราคาว่าง", Price: 0, Unit: DefaultUnit},
		// no OilName: no name, dropped.
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant %+v", got, want)
	}
}

func TestParseOtherShapes(t *testing.T) {
	cases := map[string]struct {
		body string
		want []Item
	}{
		"raw array with alternate keys": {`[{"fuel_name_th":" ดีเซล ","Fuel_Name_En":"Diesel","Price":"31.5","price_unit":"THB/L"},
			{"name":"E20","pricePerLiter":"0x1F"},{"NameTh":"x","price":"abc"},{"NameTh":"y","price":"Infinity"},7,null]`,
			[]Item{{NameTh: "ดีเซล", NameEn: "Diesel", Price: 31.5, Unit: "THB/L"}, {NameTh: "E20", NameEn: "E20", Price: 31, Unit: DefaultUnit}}},
		"data object": {`{"data":[{"name_en":"Gasohol","price":true},{"ชื่อ":"น้ำมัน","ราคาน้ำมัน":["12.5"],"หน่วย":"บาท"}]}`,
			[]Item{{NameEn: "Gasohol", Price: 1, Unit: DefaultUnit}, {NameTh: "น้ำมัน", Price: 12.5, Unit: "บาท"}}},
		"null price is 0, absent price falls through": {`[{"name":"a","price":null,"Price":"9"},{"name":"b","Price":"9"}]`,
			[]Item{{NameTh: "a", NameEn: "a", Price: 0, Unit: DefaultUnit}, {NameTh: "b", NameEn: "b", Price: 9, Unit: DefaultUnit}}},
		"broken OilList falls back to the array": {`[{"OilList":"not json","name":"z","price":1}]`,
			[]Item{{NameTh: "z", NameEn: "z", Price: 1, Unit: DefaultUnit}}},
		"blank OilList is the raw array":   {`[{"OilList":"  ","name":"q","price":"2"}]`, []Item{{NameTh: "q", NameEn: "q", Price: 2, Unit: DefaultUnit}}},
		"object without data":              {`{"items":[{"name":"a","price":1}]}`, []Item{}},
		"scalar":                           {`42`, []Item{}},
		"multi-element array price is NaN": {`[{"name":"a","price":[1,2]},{"name":"b","price":[]}]`, []Item{{NameTh: "b", NameEn: "b", Price: 0, Unit: DefaultUnit}}},
		"hex underscore and Go-only forms": {`[{"name":"a","price":"0x1_0"},{"name":"b","price":"inf"},{"name":"c","price":"1_000"},{"name":"d","price":" +.5 "}]`,
			[]Item{{NameTh: "d", NameEn: "d", Price: 0.5, Unit: DefaultUnit}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Parse([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse = %+v\nwant %+v", got, tc.want)
			}
		})
	}
	if _, err := Parse([]byte("<html>")); err == nil {
		t.Fatal("a body that is not JSON parsed")
	}
}

func TestRetailPricesRequest(t *testing.T) {
	var path, accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, accept = r.URL.Path, r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, oilListBody)
	}))
	defer srv.Close()
	at := time.Date(2026, 10, 10, 22, 0, 0, 123456789, time.UTC)
	c, err := New(Config{BaseURL: srv.URL + "/bangchak/", Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.RetailPrices(context.Background(), LocaleEN)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/bangchak/ApiOilPrice2/en" || accept != "application/json" {
		t.Fatalf("request %s (Accept %q)", path, accept)
	}
	if p.Locale != LocaleEN || p.Source != Source || !p.FetchedAt.Equal(at.Truncate(time.Millisecond)) || len(p.Items) != 3 {
		t.Fatalf("prices %+v", p)
	}
	if _, err := c.RetailPrices(context.Background(), "jp"); !errors.Is(err, ErrBadLocale) {
		t.Fatalf("locale jp: %v", err)
	}
}

func TestRetailPricesFailures(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"status": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }, "bangchak: API HTTP 502"},
		"html":   {func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "<html>down</html>") }, "not JSON"},
		"large": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "["+strings.Repeat(" ", maxBody)+"]")
		}, "too large"},
		"slow": {func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}, "request failed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			c, err := New(Config{BaseURL: srv.URL, Timeout: 200 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.RetailPrices(context.Background(), LocaleTH)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("the error carries the base URL: %v", err)
			}
		})
	}
}

func TestValidateBaseURL(t *testing.T) {
	for _, ok := range []string{"https://oil-price.bangchak.co.th", "http://mocks:8080/bangchak"} {
		if err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "oil-price.bangchak.co.th", "ftp://x", "https://u:p@x", "https://x?a=1", "https://x#f"} {
		if err := ValidateBaseURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("New without a base URL")
	}
}

// The local stack's WireMock answer (deploy/mocks, profile mocks; .env.example points BANGCHAK_API_BASE_URL at it) is
// in today's format and parses.
func TestLocalMockParses(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/mocks/mappings/bangchak.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Request struct {
			URLPathPattern string `json:"urlPathPattern"`
		} `json:"request"`
		Response struct {
			JSONBody json.RawMessage `json:"jsonBody"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	items, err := Parse(m.Response.JSONBody)
	if err != nil || len(items) != 3 || items[0].Price != 32.94 || m.Request.URLPathPattern != "/bangchak/ApiOilPrice2/(th|en)" {
		t.Fatalf("mock: %+v %v (%s)", items, err, m.Request.URLPathPattern)
	}
}

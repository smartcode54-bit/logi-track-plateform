package httpx

import (
	"reflect"
	"testing"
)

type fleetFileCollection struct {
	Keys []string `json:"keys"`
}

var fleetFileCollectionSchema = NewSchema[fleetFileCollection]("test.fleet-files", map[string]Field{"keys": {Kind: File}})

func TestFleetFileCollectionWireAndSharedTypeRetainShape(t *testing.T) {
	p := projection{audience: ProjectionAudience{Support: true, Tenant: "selected", Consents: map[string]Consent{"selected": {Enabled: true}}}, owners: map[string]bool{}}
	result, err := p.walk(reflect.ValueOf(fleetFileCollection{Keys: []string{"KEY1", "KEY2"}}), fleetFileCollectionSchema, "/data", "selected")
	if err != nil {
		t.Fatal(err)
	}
	if typ := schemaTypeScript(uuidType, Field{Kind: File}); typ != "null" {
		t.Fatal("file ID lost null shape", typ)
	}
	keys := result.(map[string]any)["keys"].([]any)
	if len(keys) != 2 || keys[0] != nil || keys[1] != nil || len(p.redacted) != 2 {
		t.Fatal(result, p.redacted)
	}
	if typ := schemaTypeScript(reflect.TypeFor[[]string](), Field{Kind: File}); typ != "Array<null> | null" {
		t.Fatal("wire/type shape mismatch", typ)
	}
}

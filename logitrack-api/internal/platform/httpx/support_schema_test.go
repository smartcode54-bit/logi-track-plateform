package httpx

import (
	"reflect"
	"strings"
	"testing"
)

type unsupportedZero struct {
	Value *string `json:"value,omitzero"`
}
type unsupportedString struct {
	Value int `json:"value,string"`
}
type contractCollision struct {
	Value string `json:"value"`
}

func TestSchemaRefusesUnsupportedJSONOptions(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[unsupportedZero](), reflect.TypeFor[unsupportedString]()} {
		schema := &ResponseSchema{id: "unsupported", typ: typ, fields: map[string]Field{"value": {Kind: Safe}}}
		if err := validateSchema(typ, schema, map[reflect.Type]bool{}); err == nil || !strings.Contains(err.Error(), "unsupported JSON options") {
			t.Fatal(typ, err)
		}
	}
}
func TestSensitiveTypesAreNullableInContracts(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		kind FieldKind
		want string
	}{
		{reflect.TypeFor[float64](), Address, "number | null"},
		{reflect.TypeFor[*float64](), Address, "number | null"},
		{reflect.TypeFor[int](), Person, "number | null"},
		{reflect.TypeFor[bool](), FreeText, "boolean | null"},
		{timeType, Person, "string | null"},
		{reflect.TypeFor[float64](), Safe, "number"},
	} {
		if got := schemaTypeScript(tc.typ, Field{Kind: tc.kind}); got != tc.want {
			t.Fatalf("%v %s: %s want %s", tc.typ, tc.kind, got, tc.want)
		}
	}
}
func TestSchemaInterfaceNamesUseIDsAndRejectCollisions(t *testing.T) {
	a := NewSchema[contractCollision]("contract.a", map[string]Field{"value": {Kind: Safe}})
	b := NewSchema[contractCollision]("contract.b", map[string]Field{"value": {Kind: Safe}})
	defer responseSchemas.Delete("contract.a")
	defer responseSchemas.Delete("contract.b")
	if schemaContractName(a) == schemaContractName(b) {
		t.Fatal("bare Go names collide")
	}
	NewSchema[contractCollision]("contract-a", map[string]Field{"value": {Kind: Safe}})
	defer responseSchemas.Delete("contract-a")
	if _, err := SchemaContracts(); err == nil || !strings.Contains(err.Error(), "duplicate schema interface") {
		t.Fatal(err)
	}
}

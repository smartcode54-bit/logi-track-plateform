package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type FieldKind string

const (
	Safe     FieldKind = "safe"
	Person   FieldKind = "person"
	Money    FieldKind = "money"
	Address  FieldKind = "address"
	File     FieldKind = "file"
	FreeText FieldKind = "free_text"
	Secret   FieldKind = "secret"
	Nested   FieldKind = "nested"
)

type Field struct {
	Kind   FieldKind
	Schema *ResponseSchema
	// MapKeys is an explicit reviewed set, never a field-name or code-shape heuristic.
	MapKeys []string
}

// ResponseSchema is immutable after construction; the full Go type is checked,
// including embedded and omitted fields, on every projected serialization.
// Raw JSON, dynamic interfaces and custom marshalers require a typed adapter.
type ResponseSchema struct {
	id     string
	typ    reflect.Type
	fields map[string]Field
	owner  func(reflect.Value) string
}

var responseSchemas sync.Map

func NewSchema[T any](id string, fields map[string]Field, owner ...func(T) string) *ResponseSchema {
	s := &ResponseSchema{id: id, typ: reflect.TypeFor[T](), fields: make(map[string]Field, len(fields))}
	for name, field := range fields {
		field.MapKeys = slices.Clone(field.MapKeys)
		s.fields[name] = field
	}
	if len(owner) > 0 {
		resolver := owner[0]
		s.owner = func(v reflect.Value) string { return resolver(v.Interface().(T)) }
	}
	if id == "" {
		panic("httpx: schema needs an id")
	}
	if _, loaded := responseSchemas.LoadOrStore(id, s); loaded {
		panic("httpx: duplicate response schema " + id)
	}
	return s
}

func SchemaIDs() []string {
	var ids []string
	responseSchemas.Range(func(k, _ any) bool { ids = append(ids, k.(string)); return true })
	sort.Strings(ids)
	return ids
}

type RedactedField struct {
	Path string    `json:"path"`
	Kind FieldKind `json:"kind"`
}
type SupportAccessMeta struct {
	Masked  bool   `json:"masked"`
	Version string `json:"version"`
}
type ProjectionMeta struct {
	SupportAccess *SupportAccessMeta `json:"supportAccess,omitempty"`
	Redacted      []RedactedField    `json:"redacted"`
}

type jsonField struct {
	name  string
	index []int
	typ   reflect.Type
	omit  bool
}

func visibleFields(t reflect.Type) ([]jsonField, error) {
	var out []jsonField
	seen := map[string]bool{}
	var visit func(reflect.Type, []int) error
	visit = func(t reflect.Type, prefix []int) error {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" && !f.Anonymous {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			if slices.Contains(tag[1:], "omitzero") || slices.Contains(tag[1:], "string") {
				return fmt.Errorf("unsupported JSON options on %s", f.Name)
			}
			idx := append(slices.Clone(prefix), i)
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if f.Anonymous && tag[0] == "" && ft.Kind() == reflect.Struct {
				if err := visit(ft, idx); err != nil {
					return err
				}
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			if seen[name] {
				return fmt.Errorf("ambiguous JSON field %s", name)
			}
			seen[name] = true
			out = append(out, jsonField{name, idx, f.Type, slices.Contains(tag[1:], "omitempty")})
		}
		return nil
	}
	return out, visit(t, nil)
}

var timeType = reflect.TypeFor[time.Time]()
var uuidType = reflect.TypeFor[uuid.UUID]()
var rawType = reflect.TypeFor[json.RawMessage]()
var marshalerType = reflect.TypeFor[json.Marshaler]()

func plainType(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType || t == uuidType {
		return true
	}
	if t == rawType || t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		return false
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	case reflect.Array, reflect.Slice:
		return plainType(t.Elem())
	}
	return false
}

func validateSchema(t reflect.Type, s *ResponseSchema, stack map[reflect.Type]bool) error {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if s == nil || t != s.typ || t.Kind() != reflect.Struct || t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		return fmt.Errorf("unclassified response type %v", t)
	}
	if stack[t] {
		return fmt.Errorf("recursive response type %v", t)
	}
	stack[t] = true
	defer delete(stack, t)
	fields, err := visibleFields(t)
	if err != nil {
		return err
	}
	if len(fields) != len(s.fields) {
		return fmt.Errorf("incomplete schema %s", s.id)
	}
	for _, f := range fields {
		rule, ok := s.fields[f.name]
		if !ok {
			return fmt.Errorf("unclassified %s.%s", s.id, f.name)
		}
		ft := f.typ
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Map {
			if ft.Key().Kind() != reflect.String || len(rule.MapKeys) == 0 {
				return fmt.Errorf("unclassified map keys %s", f.name)
			}
			ft = ft.Elem()
		}
		switch rule.Kind {
		case Nested:
			if err := validateSchema(ft, rule.Schema, stack); err != nil {
				return err
			}
		case Safe, Person, Money, Address, FreeText:
			if !plainType(ft) {
				return fmt.Errorf("unclassified nested field %s", f.name)
			}
		case File, Secret: // The entire classified opaque value is removed, even ON.
		default:
			return fmt.Errorf("unknown classification %s", rule.Kind)
		}
	}
	return nil
}

type projection struct {
	platform bool
	audience ProjectionAudience
	redacted []RedactedField
	owners   map[string]bool
}

func pointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func fieldValue(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}
func nilValue(v reflect.Value) bool {
	return !v.IsValid() || ((v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface || v.Kind() == reflect.Map || v.Kind() == reflect.Slice) && v.IsNil())
}
func emptyValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}
func (p *projection) clear(owner string) bool {
	c, ok := p.audience.Consents[owner]
	return owner != "" && ok && c.Enabled && (!p.audience.Support || p.platform || owner == p.audience.Tenant)
}
func (p *projection) walk(v reflect.Value, s *ResponseSchema, path, owner string) (any, error) {
	if nilValue(v) {
		return nil, nil
	}
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		rows := make([]any, v.Len())
		if len(rows) > 100 {
			return nil, fmt.Errorf("projection page exceeds 100 rows")
		}
		for i := range rows {
			var err error
			rows[i], err = p.walk(v.Index(i), s, fmt.Sprintf("%s/%d", path, i), owner)
			if err != nil {
				return nil, err
			}
		}
		return rows, nil
	case reflect.Map:
		return nil, fmt.Errorf("map root requires a typed adapter")
	}
	if s.owner != nil {
		owner = s.owner(v)
	}
	if owner != "" {
		p.owners[owner] = true
	}
	fields, err := visibleFields(v.Type())
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		rule := s.fields[f.name]
		value := fieldValue(v, f.index)
		fp := path + "/" + pointerPart(f.name)
		if rule.Kind == Safe && f.omit && emptyValue(value) {
			continue
		}
		item, err := p.field(value, rule, fp, owner)
		if err != nil {
			return nil, err
		}
		out[f.name] = item
	}
	return out, nil
}
func (p *projection) field(v reflect.Value, rule Field, path, owner string) (any, error) {
	for v.IsValid() && v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if v.IsValid() && (v.Kind() == reflect.Array || v.Kind() == reflect.Slice) && v.Type() != uuidType && rule.Kind != Safe && rule.Kind != Secret {
		if v.Kind() == reflect.Slice && v.IsNil() {
			if rule.Kind != Nested && (rule.Kind == File || !p.clear(owner)) {
				p.redacted = append(p.redacted, RedactedField{path, rule.Kind})
			}
			return nil, nil
		}
		items := make([]any, v.Len())
		for i := range items {
			var err error
			items[i], err = p.field(v.Index(i), rule, fmt.Sprintf("%s/%d", path, i), owner)
			if err != nil {
				return nil, err
			}
		}
		return items, nil
	}
	if v.IsValid() && v.Kind() == reflect.Map && !v.IsNil() {
		out := map[string]any{}
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, key := range keys {
			if !slices.Contains(rule.MapKeys, key.String()) {
				return nil, fmt.Errorf("unreviewed map key")
			}
			val, err := p.field(v.MapIndex(key), Field{Kind: rule.Kind, Schema: rule.Schema}, path+"/"+pointerPart(key.String()), owner)
			if err != nil {
				return nil, err
			}
			out[key.String()] = val
		}
		return out, nil
	}
	if rule.Kind == Nested {
		return p.walk(v, rule.Schema, path, owner)
	}
	if rule.Kind == Secret || rule.Kind == File || (rule.Kind != Safe && !p.clear(owner)) {
		p.redacted = append(p.redacted, RedactedField{path, rule.Kind})
		if (rule.Kind == Person || rule.Kind == Address || rule.Kind == FreeText) && stringValueType(v) {
			return "****", nil
		}
		return nil, nil
	}
	if nilValue(v) {
		return nil, nil
	}
	return v.Interface(), nil
}

func stringValueType(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	t := v.Type()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.String
}

func projectionRequired(c fiber.Ctx) bool {
	a := ProjectionAudienceFrom(c)
	p := EndpointPolicyFrom(c)
	required, _ := c.Locals(requiredKey).(bool)
	return required || a.Support || (a.Platform && p.Plane == "platform" && p.Phase == 1)
}

func projectResponse(c fiber.Ctx, status int, value any, cursor string, meta any) error {
	p := EndpointPolicyFrom(c)
	a := ProjectionAudienceFrom(c)
	registered, ok := responseSchemas.Load(p.SchemaID)
	if !ok || a.Finalize == nil {
		return ErrSupportProjectionUnavailable()
	}
	schema := registered.(*ResponseSchema)
	if value == nil || validateSchema(reflect.TypeOf(value), schema, map[reflect.Type]bool{}) != nil {
		return ErrSupportProjectionUnavailable()
	}
	var metaSchema *ResponseSchema
	if meta != nil {
		found, ok := responseSchemas.Load(p.MetaSchemaID)
		if !ok {
			return ErrSupportProjectionUnavailable()
		}
		metaSchema = found.(*ResponseSchema)
		if validateSchema(reflect.TypeOf(meta), metaSchema, map[reflect.Type]bool{}) != nil {
			return ErrSupportProjectionUnavailable()
		}
		if _, ok := metaSchema.fields["supportAccess"]; ok {
			return ErrSupportProjectionUnavailable()
		}
		if _, ok := metaSchema.fields["redacted"]; ok {
			return ErrSupportProjectionUnavailable()
		}
	}
	if cursor != "" && (p.CursorID == "" || p.ValidateCursor == nil || !p.ValidateCursor(cursor, a)) {
		return ErrSupportProjectionUnavailable()
	}
	owner := PolicyTargetFrom(c).TenantID
	if p.Target == "selected_tenant" {
		owner = a.Tenant
	}
	pre := &projection{platform: p.Plane == "platform", audience: a, owners: map[string]bool{}}
	if _, err := pre.walk(reflect.ValueOf(value), schema, "/data", owner); err != nil || len(pre.owners) > 100 {
		return ErrSupportProjectionUnavailable()
	}
	if meta != nil {
		if _, err := pre.walk(reflect.ValueOf(meta), metaSchema, "/meta", owner); err != nil {
			return ErrSupportProjectionUnavailable()
		}
	}
	if len(pre.owners) > 100 {
		return ErrSupportProjectionUnavailable()
	}
	if a.Support {
		pre.owners[a.Tenant] = true
	}
	if len(pre.owners) > 100 {
		return ErrSupportProjectionUnavailable()
	}
	owners := make([]string, 0, len(pre.owners))
	for id := range pre.owners {
		owners = append(owners, id)
	}
	sort.Strings(owners)
	err := a.Finalize(c.Context(), owners, func(consents map[string]Consent) error {
		if a.Support {
			current, ok := consents[a.Tenant]
			if !ok || current.Version != a.ConsentVersion {
				return NewError(http.StatusUnauthorized, "token_expired", "token expired").WithDetails(map[string]any{"reason": "claims_changed"})
			}
		}
		a.Consents = consents
		projected := &projection{platform: p.Plane == "platform", audience: a, owners: map[string]bool{}, redacted: []RedactedField{}}
		data, err := projected.walk(reflect.ValueOf(value), schema, "/data", owner)
		if err != nil {
			return ErrSupportProjectionUnavailable()
		}
		metadata := map[string]any{}
		if meta != nil {
			result, err := projected.walk(reflect.ValueOf(meta), metaSchema, "/meta", owner)
			if err != nil {
				return ErrSupportProjectionUnavailable()
			}
			metadata = result.(map[string]any)
		}
		metadata["redacted"] = projected.redacted
		if a.Support {
			metadata["supportAccess"] = SupportAccessMeta{Masked: !consents[a.Tenant].Enabled, Version: a.ConsentVersion}
		}

		cleanProjectionHeaders(c)
		if err := c.Status(status).JSON(dataBody{Data: data, NextCursor: cursor, Meta: metadata}); err != nil {
			return err
		}
		sealResponse(c)
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func cleanProjectionHeaders(c fiber.Ctx) {
	id := RequestIDFrom(c)
	c.Response().Header.Reset()
	if id != "" {
		c.Set(HeaderRequestID, id)
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	c.Set("X-Content-Type-Options", "nosniff")
}

type projectionReceipt struct{ digest [32]byte }

func responseHeadDigest(c fiber.Ctx) [32]byte {
	h := sha256.New()
	p := EndpointPolicyFrom(c)
	a := ProjectionAudienceFrom(c)
	fmt.Fprintf(h, "%q %q %q %q %q %q %q %d\n", p.ID, p.SchemaID, a.Actor, a.Session, a.Tenant, a.ContextVersion, a.ConsentVersion, c.Response().StatusCode())
	policyBytes, _ := json.Marshal(p)
	h.Write(policyBytes)
	targetBytes, _ := json.Marshal(PolicyTargetFrom(c))
	h.Write(targetBytes)
	fmt.Fprintf(h, "mode %t %t\n", a.Support, a.Platform)
	var headers []string
	c.Response().Header.VisitAll(func(k, v []byte) { headers = append(headers, string(k)+":"+string(v)) })
	sort.Strings(headers)
	for _, line := range headers {
		fmt.Fprintln(h, line)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func responseDigest(c fiber.Ctx) [32]byte {
	head := responseHeadDigest(c)
	h := sha256.New()
	h.Write(head[:])
	h.Write(c.Response().Body())
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func sealResponse(c fiber.Ctx) { c.Locals(receiptKey, projectionReceipt{responseDigest(c)}) }

type resolverKey struct{}
type AudienceResolver func(fiber.Ctx)

func ResolveProjectionAudience(c fiber.Ctx) {
	if resolve, ok := c.Locals(resolverKey{}).(AudienceResolver); ok && resolve != nil {
		resolve(c)
	}
}

// AdmissionGuard includes error serialization inside the buffered boundary. A
// direct writer, redirect, stream or replacement after JSON invalidates proof.
func AdmissionGuard(resolve AudienceResolver) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals(resolverKey{}, resolve)
		err := c.Next()
		ResolveProjectionAudience(c)
		if err != nil {
			if e := c.App().Config().ErrorHandler(c, err); e != nil {
				return e
			}
		}
		if !projectionRequired(c) {
			return nil
		}
		if c.RequestCtx().Hijacked() {
			c.RequestCtx().Hijack(nil)
			c.RequestCtx().HijackSetNoResponse(false)
			c.Response().Reset()
			return WriteError(c, ErrSupportProjectionUnavailable())
		}
		if c.Response().IsBodyStream() {
			if admittedStream(c) {
				return nil
			}
			c.Response().Reset()
			return WriteError(c, ErrSupportProjectionUnavailable())
		}
		receipt, ok := c.Locals(receiptKey).(projectionReceipt)
		if !ok || receipt.digest != responseDigest(c) {
			c.Response().Reset()
			return WriteError(c, ErrSupportProjectionUnavailable())
		}
		return nil
	}
}

// FreshProjection is useful to policy repositories implementing Finalize. The
// callback remains mandatory even for counts-only schemas and consent ON.
type FreshProjection func(context.Context, []string, func(map[string]Consent) error) error

// SchemaContract exposes only classified wire metadata to the shared generator.
type SchemaContract struct {
	Name   string
	Fields []SchemaFieldContract
}
type SchemaFieldContract struct {
	Name, Type string
	Optional   bool
}

func SchemaContracts() ([]SchemaContract, error) {
	var contractErr error
	names := map[string]bool{}
	var out []SchemaContract
	responseSchemas.Range(func(_, value any) bool {
		schema := value.(*ResponseSchema)
		fields, err := visibleFields(schema.typ)
		if err != nil {
			contractErr = err
			return false
		}
		name := schemaContractName(schema)
		if names[name] {
			contractErr = fmt.Errorf("duplicate schema interface %s", name)
			return false
		}
		names[name] = true
		contract := SchemaContract{Name: name}
		for _, field := range fields {
			name := schemaTypeScript(field.typ, schema.fields[field.name])
			contract.Fields = append(contract.Fields, SchemaFieldContract{Name: field.name, Type: name, Optional: field.omit})
		}
		out = append(out, contract)
		return true
	})
	slices.SortFunc(out, func(a, b SchemaContract) int { return strings.Compare(a.Name, b.Name) })
	return out, contractErr
}

func schemaContractName(s *ResponseSchema) string {
	name := ""
	for _, part := range strings.FieldsFunc(s.id, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		chars := []rune(part)
		chars[0] = unicode.ToUpper(chars[0])
		name += string(chars)
	}
	return "Response" + name
}

func schemaTypeScript(t reflect.Type, rule Field) string {
	if rule.Kind == Secret || rule.Kind == File {
		return "null"
	}
	if t.Kind() == reflect.Pointer {
		inner := schemaTypeScript(t.Elem(), rule)
		if strings.Contains(inner, "null") || inner == "MoneyValue" || inner == "DecimalMoneyValue" {
			return inner
		}
		return inner + " | null"
	}
	if t == uuidType || t == timeType {
		if rule.Kind != Safe {
			return "string | null"
		}
		return "string"
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		result := "Array<" + schemaTypeScript(t.Elem(), rule) + ">"
		if t.Kind() == reflect.Slice {
			result += " | null"
		}
		return result
	}
	if t.Kind() == reflect.Map {
		keys := make([]string, len(rule.MapKeys))
		for i, key := range rule.MapKeys {
			encoded, _ := json.Marshal(key)
			keys[i] = string(encoded)
		}
		sort.Strings(keys)
		return "Partial<Record<" + strings.Join(keys, " | ") + ", " + schemaTypeScript(t.Elem(), rule) + ">> | null"
	}
	if rule.Kind == Nested && rule.Schema != nil {
		return schemaContractName(rule.Schema)
	}
	if rule.Kind == Money {
		if t.Kind() == reflect.String {
			return "DecimalMoneyValue"
		}
		return "MoneyValue"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		if rule.Kind != Safe {
			return "boolean | null"
		}
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		if rule.Kind != Safe {
			return "number | null"
		}
		return "number"
	}
	return "unknown"
}

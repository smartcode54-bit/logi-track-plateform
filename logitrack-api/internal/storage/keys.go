package storage

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// PublicPrefix is the only prefix of the public bucket (R23, ADR 0007): APKs. Every other key lives in the
// private bucket. The local backend serves it without a signature; everything else needs one.
const PublicPrefix = "app_releases/"

// MaxKeyBytes bounds an object key (S3 allows 1024 bytes).
const MaxKeyBytes = 1024

// ErrInvalidKey is returned for a key that is empty, too long, absolute, has an empty, "." or ".." segment,
// a backslash, a control character or invalid UTF-8. Such a key never reaches a backend: on the local backend it
// would escape LOCAL_MEDIA_DIR, on S3 it would not round-trip through a URL path.
var ErrInvalidKey = errors.New("storage: invalid object key")

// ValidateKey checks the key rules every backend shares (main spec §9.2). Legacy keys copied by the ETL keep their
// Firebase spelling (spaces, Thai file names), so the check refuses only what is unsafe, not unusual characters.
func ValidateKey(key string) error {
	if key == "" || len(key) > MaxKeyBytes || !utf8.ValidString(key) || strings.HasPrefix(key, "/") ||
		strings.HasSuffix(key, "/") || strings.Contains(key, "\\") {
		return ErrInvalidKey
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return ErrInvalidKey
		}
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > 255 {
			return ErrInvalidKey
		}
	}
	return nil
}

// IsPublicKey reports whether key belongs to the public bucket (app_releases/).
func IsPublicKey(key string) bool { return strings.HasPrefix(key, PublicPrefix) }

// Owner kinds of file_objects.owner_kind (Appendix A §A.2.1).
const (
	OwnerTrip        = "trip"
	OwnerTask        = "task"
	OwnerStandby     = "standby"
	OwnerIncident    = "incident"
	OwnerChat        = "chat"
	OwnerDriver      = "driver"
	OwnerTruck       = "truck"
	OwnerCompany     = "company"
	OwnerCustomer    = "customer"
	OwnerTenant      = "tenant"
	OwnerMaintenance = "maintenance"
	OwnerExpense     = "expense"
	OwnerLeave       = "leave"
	OwnerRelease     = "release"
	OwnerUser        = "user"
	OwnerStatement   = "statement"
	OwnerReport      = "report"
	OwnerPenalty     = "penalty"
)

// Content types accepted from clients and the extension each gets in a new key.
var extByType = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/webp":      "webp",
	"application/pdf": "pdf",
}

var (
	imageTypes    = []string{"image/jpeg", "image/png", "image/webp"}
	documentTypes = []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}
)

// KeyInput is what a purpose template turns into a key: the entity, an optional variant (photo type, document
// kind, attachment index) and an optional original file name, plus the upload instant.
type KeyInput struct {
	EntityID    uuid.UUID
	Variant     string
	FileName    string
	ContentType string
	Now         time.Time
}

// Purpose is one row of the purpose vocabulary (Appendix A §A.2.1, main spec §9.2): the owner kind the entity
// commit links, the content types a client may upload, and the key template. Presign is false for server-side
// purposes (rendered documents, reports, APKs), which no client presigns through POST /v1/uploads/presign.
type Purpose struct {
	Name         string
	OwnerKind    string
	ContentTypes []string
	Presign      bool
	// EntityOptional: the entity may be omitted (user_photo defaults to the caller).
	EntityOptional bool
	// Variants: the allowed variants (nil = no variant); VariantRx: a pattern instead of a list.
	Variants  []string
	VariantRx *regexp.Regexp
	// DefaultVariant fills an omitted variant ("" = required when Variants or VariantRx is set).
	DefaultVariant string
	key            func(in KeyInput, ext string) string
}

// Key builds the object key of a new upload.
func (p Purpose) Key(in KeyInput) (string, error) {
	ext, ok := extByType[in.ContentType]
	if !ok || p.key == nil {
		return "", fmt.Errorf("storage: no key template for %s / %s", p.Name, in.ContentType)
	}
	if in.Variant == "" {
		in.Variant = p.DefaultVariant
	}
	key := p.key(in, ext)
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// ValidVariant reports whether v is acceptable for p ("" stands for the default).
func (p Purpose) ValidVariant(v string) bool {
	switch {
	case v == "":
		return p.DefaultVariant != "" || (p.Variants == nil && p.VariantRx == nil)
	case p.VariantRx != nil:
		return p.VariantRx.MatchString(v)
	default:
		for _, x := range p.Variants {
			if x == v {
				return true
			}
		}
		return false
	}
}

// AcceptsType reports whether a client may upload contentType for p.
func (p Purpose) AcceptsType(contentType string) bool {
	for _, t := range p.ContentTypes {
		if t == contentType {
			return true
		}
	}
	return false
}

func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

// fileNameRx keeps the safe characters of an original file name; the rest becomes "_".
var fileNameRx = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeName is the {name} part of a key: the original file name without its extension, reduced to
// [A-Za-z0-9._-], at most 64 bytes, "file" when nothing is left. The extension always comes from the content type.
func safeName(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	name = strings.Trim(fileNameRx.ReplaceAllString(name, "_"), "._-")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		return "file"
	}
	return name
}

// tripPhotoRx is a trip photo type: a known type of TRIP_PHOTO_TYPE_ENUM or stop_{i}_{step} (free text in
// trip_photos.photo_type, R19), lower snake case.
var tripPhotoRx = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// indexRx is an attachment index (leave evidence {ms}_{i}).
var indexRx = regexp.MustCompile(`^[0-9]{1,3}$`)

// Purposes is the vocabulary of file_objects.purpose with the key layout of main spec §9.2. New keys use entity
// uuids and a millisecond stamp, so a replaced photo gets a new key and keeps its old row (§9.3).
var Purposes = func() map[string]Purpose {
	list := []Purpose{
		{Name: "trip_photo", OwnerKind: OwnerTrip, ContentTypes: imageTypes, Presign: true, VariantRx: tripPhotoRx,
			key: func(in KeyInput, ext string) string {
				return "trips/" + in.EntityID.String() + "/" + in.Variant + "-" + ms(in.Now) + "." + ext
			}},
		{Name: "checkin_photo", OwnerKind: OwnerTask, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "checkin/" + in.EntityID.String() + "/" + ms(in.Now) + "." + ext
			}},
		{Name: "checkin_app_screenshot", OwnerKind: OwnerTask, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "checkin/" + in.EntityID.String() + "/app_screenshot_" + ms(in.Now) + "." + ext
			}},
		{Name: "standby_photo", OwnerKind: OwnerStandby, ContentTypes: imageTypes, Presign: true,
			Variants: []string{"customer_worksheet", "site_photo"},
			key: func(in KeyInput, ext string) string {
				return "standby/" + in.EntityID.String() + "/" + in.Variant + "-" + ms(in.Now) + "." + ext
			}},
		{Name: "incident_photo", OwnerKind: OwnerIncident, ContentTypes: imageTypes, Presign: true,
			Variants: []string{"map", "situation1", "situation2"},
			key: func(in KeyInput, ext string) string {
				return "incidents/" + in.EntityID.String() + "/" + in.Variant + "-" + ms(in.Now) + "." + ext
			}},
		{Name: "chat_image", OwnerKind: OwnerChat, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "chats/" + in.EntityID.String() + "/" + ms(in.Now) + "." + ext
			}},
		{Name: "leave_evidence", OwnerKind: OwnerLeave, ContentTypes: documentTypes, Presign: true,
			VariantRx: indexRx, DefaultVariant: "0",
			key: func(in KeyInput, ext string) string {
				return "leave/" + in.EntityID.String() + "/" + ms(in.Now) + "_" + in.Variant + "." + ext
			}},
		{Name: "maintenance_file", OwnerKind: OwnerMaintenance, ContentTypes: documentTypes, Presign: true,
			Variants: []string{"image", "receipt", "invoice"},
			key: func(in KeyInput, ext string) string {
				return "maintenance/" + in.EntityID.String() + "/" + in.Variant + "_" + ms(in.Now) + "." + ext
			}},
		{Name: "expense_receipt", OwnerKind: OwnerExpense, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "expenses/" + in.EntityID.String() + "/receipt-" + ms(in.Now) + "." + ext
			}},
		{Name: "expense_odometer", OwnerKind: OwnerExpense, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "expenses/" + in.EntityID.String() + "/odometer-" + ms(in.Now) + "." + ext
			}},
		{Name: "driver_profile", OwnerKind: OwnerDriver, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "drivers/" + in.EntityID.String() + "/profile-" + ms(in.Now) + "." + ext
			}},
		{Name: "driver_id_card", OwnerKind: OwnerDriver, ContentTypes: documentTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "drivers/" + in.EntityID.String() + "/id_card-" + ms(in.Now) + "." + ext
			}},
		{Name: "driver_license", OwnerKind: OwnerDriver, ContentTypes: documentTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "drivers/" + in.EntityID.String() + "/license-" + ms(in.Now) + "." + ext
			}},
		truckFile("truck_photo", "photos", imageTypes),
		truckFile("truck_document", "documents", documentTypes),
		truckFile("truck_receipt", "receipts", documentTypes),
		truckFile("insurance_document", "insurance", documentTypes),
		{Name: "tenant_document", OwnerKind: OwnerTenant, ContentTypes: documentTypes, Presign: true,
			Variants: []string{"id_card", "company_doc", "other"},
			key: func(in KeyInput, ext string) string {
				folder := map[string]string{"id_card": "id_cards", "company_doc": "company_docs", "other": "other"}[in.Variant]
				return "subcontractors/" + in.EntityID.String() + "/" + folder + "/" + ms(in.Now) + "_" + safeName(in.FileName) + "." + ext
			}},
		{Name: "customer_logo", OwnerKind: OwnerCustomer, ContentTypes: imageTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "customers/" + in.EntityID.String() + "/logo-" + ms(in.Now) + "." + ext
			}},
		companyAsset("company_logo", "logo"),
		companyAsset("company_stamp", "stamp"),
		companyAsset("company_signature", "signature"),
		{Name: "user_photo", OwnerKind: OwnerUser, ContentTypes: imageTypes, Presign: true, EntityOptional: true,
			key: func(in KeyInput, ext string) string {
				return "users/" + in.EntityID.String() + "/photo-" + ms(in.Now) + "." + ext
			}},
		{Name: "penalty_evidence", OwnerKind: OwnerPenalty, ContentTypes: documentTypes, Presign: true,
			key: func(in KeyInput, ext string) string {
				return "penalties/" + in.EntityID.String() + "/evidence-" + ms(in.Now) + "." + ext
			}},
		// Server-side writers only (documents.render, reports, cmd/release): never presigned by clients.
		{Name: "statement_document", OwnerKind: OwnerStatement},
		{Name: "report", OwnerKind: OwnerReport},
		{Name: "apk", OwnerKind: OwnerRelease},
	}
	out := make(map[string]Purpose, len(list))
	for _, p := range list {
		out[p.Name] = p
	}
	return out
}()

func truckFile(name, folder string, types []string) Purpose {
	return Purpose{Name: name, OwnerKind: OwnerTruck, ContentTypes: types, Presign: true,
		key: func(in KeyInput, ext string) string {
			return "trucks/" + in.EntityID.String() + "/" + folder + "/" + ms(in.Now) + "_" + safeName(in.FileName) + "." + ext
		}}
}

// companyAsset keys carry a millisecond stamp (main spec §9.2 lists companies/{id}/{logo|stamp|signature}.{ext}):
// a fixed key could not take a second upload, since (bucket, object_key) is unique and a replaced file keeps its row.
func companyAsset(name, kind string) Purpose {
	return Purpose{Name: name, OwnerKind: OwnerCompany, ContentTypes: imageTypes, Presign: true,
		key: func(in KeyInput, ext string) string {
			return "companies/" + in.EntityID.String() + "/" + kind + "-" + ms(in.Now) + "." + ext
		}}
}

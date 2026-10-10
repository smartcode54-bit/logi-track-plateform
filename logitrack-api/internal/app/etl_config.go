package app

import (
	"strings"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// ETLConfig is the cmd/etl configuration (main spec §13, §16.1): exactly the names whose consumer column lists
// etl. OWN_FLEET_TENANT_ID is required for every command (R56); the other groups are required by the commands
// that use them (RequireDB, RequireFirestore, RequireMedia), so `etl load` runs without Google credentials and
// `etl dump` without a database.
type ETLConfig struct {
	Common
	DatabaseURL         string `env:"ETL_DATABASE_URL"`
	MaxConns            int32  `env:"DATABASE_MAX_CONNS"`
	MinConns            int32  `env:"DATABASE_MIN_CONNS"`
	OwnFleetTenantID    string `env:"OWN_FLEET_TENANT_ID,required,notEmpty"`
	FirestoreProjectID  string `env:"ETL_FIRESTORE_PROJECT_ID"`
	FirestoreDatabaseID string `env:"FIRESTORE_DATABASE_ID" envDefault:"(default)"`
	GCSBucket           string `env:"ETL_GCS_BUCKET"`
	GoogleCredentials   string `env:"GOOGLE_APPLICATION_CREDENTIALS"`
	S3Endpoint          string `env:"S3_ENDPOINT"`
	S3Region            string `env:"S3_REGION"`
	S3AccessKeyID       string `env:"S3_ACCESS_KEY_ID"`
	S3SecretAccessKey   string `env:"S3_SECRET_ACCESS_KEY"`
	S3Bucket            string `env:"S3_BUCKET" envDefault:"logitrack"`
	S3UsePathStyle      bool   `env:"S3_USE_PATH_STYLE" envDefault:"true"`
	S3UseSSL            bool   `env:"S3_USE_SSL"`
	// PlatformAdminEmails marks the auth-import report lines that seed bootstrap-platform-admins grants (T19).
	PlatformAdminEmails string `env:"PLATFORM_ADMIN_EMAILS"`
	// FIREBASE_SCRYPT_* are the Console "Password hash parameters" of auth-weak-scan (Appendix C §C.5.2, §C.5.7).
	ScryptSignerKey string `env:"FIREBASE_SCRYPT_SIGNER_KEY"`
	ScryptSaltSep   string `env:"FIREBASE_SCRYPT_SALT_SEPARATOR"`
	ScryptRounds    int    `env:"FIREBASE_SCRYPT_ROUNDS"`
	ScryptMemCost   int    `env:"FIREBASE_SCRYPT_MEM_COST"`

	// Parsed by Validate.
	OwnFleet    uuid.UUID              `env:"-"`
	Scrypt      *firebasescrypt.Params `env:"-"`
	AdminEmails []string               `env:"-"`
}

// Validate implements config.Validator.
func (c *ETLConfig) Validate() error {
	var errs []string
	c.validate(&errs) // Common.validate
	if c.DatabaseURL != "" && !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		errs = append(errs, config.Invalidf("ETL_DATABASE_URL", "must be a postgres:// URL"))
	}
	if c.MaxConns < 0 || c.MinConns < 0 || (c.MaxConns > 0 && c.MinConns > c.MaxConns) {
		errs = append(errs, "DATABASE_MAX_CONNS, DATABASE_MIN_CONNS: must be >= 0 with MIN <= MAX")
	}
	if c.OwnFleetTenantID != "" {
		id, err := uuid.Parse(c.OwnFleetTenantID)
		switch {
		case err != nil || id == uuid.Nil:
			errs = append(errs, config.Invalidf("OWN_FLEET_TENANT_ID", "must be a uuid"))
		case id.String() == tenancy.QuarantineTenantID:
			errs = append(errs, config.Invalidf("OWN_FLEET_TENANT_ID", "must not be the quarantine tenant id"))
		default:
			c.OwnFleet = id
		}
	}
	if !bucketRx.MatchString(c.S3Bucket) {
		errs = append(errs, config.Invalidf("S3_BUCKET", "must be a bucket name (3-63 lower-case letters, digits, dots, hyphens)"))
	}
	if c.S3Endpoint != "" && (c.S3Region == "" || c.S3AccessKeyID == "" || c.S3SecretAccessKey == "") {
		errs = append(errs, "S3_REGION, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY: required with S3_ENDPOINT")
	}
	sp, err := firebasescrypt.ParseParams(c.ScryptSignerKey, c.ScryptSaltSep, c.ScryptRounds, c.ScryptMemCost)
	if err != nil {
		errs = append(errs, err.Error()+" (set all four FIREBASE_SCRYPT_* or none)")
	}
	c.Scrypt = sp
	emails, bad := iam.ParseEmails(c.PlatformAdminEmails)
	if len(bad) > 0 {
		errs = append(errs, config.Invalidf("PLATFORM_ADMIN_EMAILS", "must be a comma list of email addresses"))
	}
	c.AdminEmails = emails
	if len(errs) > 0 {
		return &config.Error{Invalid: errs}
	}
	return nil
}

// RequireDB checks the names of the database commands.
func (c *ETLConfig) RequireDB() error {
	if c.DatabaseURL == "" {
		return &config.Error{Missing: []string{"ETL_DATABASE_URL"}}
	}
	return nil
}

// RequireFirestore checks the names of the commands that read or write Firestore (dump, export-back).
func (c *ETLConfig) RequireFirestore() error {
	var missing []string
	if c.FirestoreProjectID == "" {
		missing = append(missing, "ETL_FIRESTORE_PROJECT_ID")
	}
	if c.GoogleCredentials == "" {
		missing = append(missing, "GOOGLE_APPLICATION_CREDENTIALS")
	}
	if len(missing) > 0 {
		return &config.Error{Missing: missing}
	}
	return nil
}

// RequireScrypt checks the names of auth-weak-scan (the Firebase Console hash parameters).
func (c *ETLConfig) RequireScrypt() error {
	if c.Scrypt == nil {
		return &config.Error{Missing: []string{"FIREBASE_SCRYPT_SIGNER_KEY", "FIREBASE_SCRYPT_SALT_SEPARATOR",
			"FIREBASE_SCRYPT_ROUNDS", "FIREBASE_SCRYPT_MEM_COST"}}
	}
	return nil
}

// RequireS3 checks the names of the commands that write the object store (dump --out=s3, media-copy).
func (c *ETLConfig) RequireS3() error {
	if c.S3Endpoint == "" {
		return &config.Error{Missing: []string{"S3_ENDPOINT"}}
	}
	return nil
}

// RequireMedia checks the names of media-copy (GCS source and S3 target).
func (c *ETLConfig) RequireMedia() error {
	var missing []string
	if c.GCSBucket == "" {
		missing = append(missing, "ETL_GCS_BUCKET")
	}
	if c.GoogleCredentials == "" {
		missing = append(missing, "GOOGLE_APPLICATION_CREDENTIALS")
	}
	if c.S3Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if len(missing) > 0 {
		return &config.Error{Missing: missing}
	}
	return nil
}

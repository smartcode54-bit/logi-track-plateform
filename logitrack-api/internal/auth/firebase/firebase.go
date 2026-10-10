// Package firebase is the Firebase side of the transitional bridge (main spec §4.9, Appendix C §C.6),
// built without the Firebase Admin SDK:
//
//   - Verifier checks Firebase ID tokens with go-oidc against the issuer
//     https://securetoken.google.com/{FIREBASE_PROJECT_ID} (audience FIREBASE_PROJECT_ID, RS256, keys from
//     the securetoken service account's JWKS);
//   - ServiceAccount.CustomToken mints Firebase custom tokens: RS256 JWTs signed with the private key of
//     the service-account file named by GOOGLE_APPLICATION_CREDENTIALS;
//   - Accounts writes account changes (password, disable / enable, refresh-token revocation through
//     validSince, custom attributes, creation) through the Identity Toolkit v1 REST API with an OAuth2
//     access token of the same service account (RFC 7523 JWT bearer grant).
//
// The wire formats follow what the Firebase Admin SDK sends (firebase.google.com/go/v4 v4.20.0,
// auth/auth.go, auth/user_mgt.go): the custom-token claims and audience, the project-scoped
// /v1/projects/{project}/accounts, accounts:lookup and accounts:update methods, and the fields localId,
// password, disableUser, validSince (seconds, as a string) and customAttributes (a JSON string).
//
// Nothing here logs. Errors never carry a request body (a password may be in it), a response body or a
// byte of the service-account file; Identity Toolkit errors keep only Google's error code.
package firebase

import (
	"errors"
	"regexp"
	"time"
)

// Endpoints and fixed values of the Firebase protocol.
const (
	// SecureTokenIssuerPrefix + project id is the iss of every Firebase ID token of that project.
	SecureTokenIssuerPrefix = "https://securetoken.google.com/"
	// SecureTokenJWKSURL publishes the keys that sign Firebase ID tokens (the JWK form of
	// https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com, which the
	// Admin SDK reads). The verifier uses it directly instead of OIDC discovery.
	SecureTokenJWKSURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"
	// CustomTokenAudience is the aud of a Firebase custom token.
	CustomTokenAudience = "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit"
	// CustomTokenTTL is the life of a custom token (the most Firebase accepts).
	CustomTokenTTL = time.Hour
	// IdentityToolkitURL is the base of the account-management methods.
	IdentityToolkitURL = "https://identitytoolkit.googleapis.com/v1"
	// DefaultTokenURL is Google's OAuth2 token endpoint, used when the key file names none.
	DefaultTokenURL = "https://oauth2.googleapis.com/token"
)

// Scopes of the OAuth2 access token Accounts uses.
var Scopes = []string{
	"https://www.googleapis.com/auth/identitytoolkit",
	"https://www.googleapis.com/auth/cloud-platform",
}

// MaxUIDLength bounds a Firebase uid (bytes, as the Admin SDK counts them).
const MaxUIDLength = 128

// MaxClaimsLength bounds the serialised developer claims of a custom token and the customAttributes of
// an account (Firebase rejects larger payloads).
const MaxClaimsLength = 1000

// ReservedClaims may not be developer claims (the Admin SDK's list).
var ReservedClaims = []string{
	"acr", "amr", "at_hash", "aud", "auth_time", "azp", "cnf", "c_hash",
	"exp", "firebase", "iat", "iss", "jti", "nbf", "nonce", "sub",
}

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// ValidProjectID reports whether id has the shape of a Google Cloud / Firebase project id: 6 to 30
// lower-case letters, digits or hyphens, starting with a letter and not ending with a hyphen.
func ValidProjectID(id string) bool { return projectIDPattern.MatchString(id) }

// ErrUnavailable means Google could not be reached (keys, token endpoint, Identity Toolkit): nothing was
// judged or written. Callers answer 503.
var ErrUnavailable = errors.New("firebase: Google unavailable")

// validUID reports whether uid can name a Firebase account.
func validUID(uid string) bool { return uid != "" && len(uid) <= MaxUIDLength }

/// Build flavor, from `--dart-define=FLAVOR=dev|prod|gotest`.
///
/// - `prod`: the fleet's app (`com.wrt.logitrack`, Firebase prod), built from `main` only.
/// - `dev`: the dev app (Firebase dev).
/// - `gotest`: the mv-go test app (`com.wrt.logitrack.gotest`). It installs next to the prod and dev apps,
///   uses Firebase dev and talks to the Go staging API at [apiBaseUrl]. It must never reach prod.
class AppFlavor {
  AppFlavor._();

  static const String name = String.fromEnvironment('FLAVOR', defaultValue: 'dev');

  static const bool isProd = name == 'prod';
  static const bool isGoTest = name == 'gotest';

  /// The fleet whose backend this build uses, as stored in `mobile_installations.flavor` and read by the version
  /// gate. The Go API accepts only `dev` / `prod` (`internal/mobileapp`), and `gotest` runs on the dev project,
  /// so it reports `dev`; its version (`4.0.0-gotest.N`) is what tells it apart.
  static const String backendFleet = isProd ? 'prod' : 'dev';

  /// Go API base URL, from `--dart-define=API_BASE_URL=...`; set for `gotest` builds only.
  static const String apiBaseUrl = String.fromEnvironment('API_BASE_URL');

  /// The Google server client id (the OAuth web client, `aud` of the ID token) for Google sign-in against Go, from
  /// `--dart-define=GOOGLE_OIDC_CLIENT_ID=...` (developer-spec §11.13); `gotest` only. Empty = Google sign-in off.
  static const String googleOidcClientId = String.fromEnvironment('GOOGLE_OIDC_CLIENT_ID');

  /// A `gotest` build without [apiBaseUrl] would silently fall back to dev-only Firebase behaviour; refuse it.
  static void assertConfigured() {
    if (isGoTest && apiBaseUrl.isEmpty) {
      throw StateError('FLAVOR=gotest needs --dart-define=API_BASE_URL (use scripts/build-mobile-gotest.mjs)');
    }
  }
}

// Command etl: Firestore and Storage to PostgreSQL and MinIO (developer-spec.md §13).
// Not implemented yet; it exits non-zero until issue T15 lands.
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	os.Exit(app.NotImplemented("etl", "T15", os.Stderr))
}

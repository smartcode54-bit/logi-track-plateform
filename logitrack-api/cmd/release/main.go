// Command release: APK publish CLI on the private network (developer-spec.md §2.1, R43, R82).
// Not implemented yet; it exits non-zero until issue T52 lands.
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	os.Exit(app.NotImplemented("release", "T52", os.Stderr))
}

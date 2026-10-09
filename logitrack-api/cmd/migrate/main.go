// Command migrate: goose migrations 0001-0010 (developer-spec.md §3, Appendix A).
// Not implemented yet; it exits non-zero until issue T03 lands.
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	os.Exit(app.NotImplemented("migrate", "T03", os.Stderr))
}

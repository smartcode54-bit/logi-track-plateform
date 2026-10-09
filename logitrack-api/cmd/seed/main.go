// Command seed: seed profiles and --verify (developer-spec.md §14, Appendix D).
// Not implemented yet; it exits non-zero until issue T16 lands.
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	os.Exit(app.NotImplemented("seed", "T16", os.Stderr))
}

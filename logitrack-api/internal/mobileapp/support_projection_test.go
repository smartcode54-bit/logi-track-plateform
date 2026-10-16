package mobileapp

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestPlatformInstallationDTOsAreCompletelyClassified(t *testing.T) {
	if _, err := httpx.SchemaContracts(); err != nil {
		t.Fatal(err)
	}
}

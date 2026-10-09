package pgtest

import (
	"os"
	"regexp"
	"testing"
)

// compose, testcontainers and CI run the same PostgreSQL image (R34).
func TestImageMatchesCompose(t *testing.T) {
	p, err := moduleFile("deploy", "docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	images := regexp.MustCompile(`(?m)^\s*image:\s*(postgres:\S+)\s*$`).FindAllStringSubmatch(string(b), -1)
	if len(images) != 1 || images[0][1] != Image {
		t.Fatalf("compose postgres images = %v, want exactly %s", images, Image)
	}
}

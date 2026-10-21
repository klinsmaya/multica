package seed_test

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/registry/seed"
)

func TestTemplatesAreValidPackages(t *testing.T) {
	list, err := seed.Templates()
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	var names []string
	for _, template := range list {
		if template.Version == "" || template.Category == "" || len(template.Archive) == 0 {
			t.Fatalf("template %s is incomplete: %+v", template.Dir, template)
		}
		names = append(names, template.Name)
	}
	if strings.Join(names, ",") != "code-review-agent,customer-support-squad,release-notes" {
		t.Fatalf("templates = %v", names)
	}
}

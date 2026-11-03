package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestStorageReadmeTracksProactiveSchema(t *testing.T) {
	contents, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(contents)
	for _, claim := range []string{fmt.Sprintf("v1–v%d", schemaVersion), fmt.Sprintf("currently **%d**", schemaVersion)} {
		if !strings.Contains(readme, claim) {
			t.Errorf("README missing current schema claim %q", claim)
		}
	}
	var identityRow string
	for _, line := range strings.Split(readme, "\n") {
		if strings.HasPrefix(line, "| v59 |") {
			identityRow = line
		}
	}
	for _, object := range []string{"stream_item_proactive_children", "stream_item_status_entries", "triggers"} {
		if !strings.Contains(identityRow, object) {
			t.Errorf("README v59 row does not document %s", object)
		}
	}
}

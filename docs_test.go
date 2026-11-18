package understudy

import (
	"strings"
	"testing"
)

func TestEmbeddedDocs(t *testing.T) {
	if !strings.HasPrefix(ScriptsGuide, "# Writing understudy scripts") {
		t.Fatalf("ScriptsGuide starts %.40q", ScriptsGuide)
	}
	if !strings.Contains(Readme, "understudy") {
		t.Fatal("Readme")
	}
}

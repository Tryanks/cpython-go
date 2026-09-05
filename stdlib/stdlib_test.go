package stdlib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeFrozenSources(t *testing.T) {
	home := t.TempDir()
	if err := materializeFrozenSources(home); err != nil {
		t.Fatal(err)
	}
	for name := range frozenSources {
		path := filepath.Join(home, "lib", "python3.14", filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if len(data) == 0 {
			t.Errorf("%s: empty source file", name)
		}
	}
}

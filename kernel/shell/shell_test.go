package shell

import (
	"io/fs"
	"testing"
)

func TestFS_HasIndexAndApp(t *testing.T) {
	f := FS()
	for _, name := range []string{"index.html", "app.js", "app.css"} {
		b, err := fs.ReadFile(f, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

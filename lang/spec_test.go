package lang

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// The specification must list exactly the keywords the lexer knows, and every
// conformance section must match a section of the specification.
func TestSpecCoversTheLanguage(t *testing.T) {
	b, err := os.ReadFile("../docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(b)
	i := strings.Index(spec, "**Keywords**")
	start := strings.Index(spec[i:], "```") + i + 3
	end := strings.Index(spec[start:], "```") + start
	listed := strings.Fields(spec[start:end])
	var known []string
	for k := range keywords {
		known = append(known, k)
	}
	sort.Strings(listed)
	sort.Strings(known)
	if strings.Join(listed, " ") != strings.Join(known, " ") {
		t.Errorf("docs/spec.md lists keywords\n  %v\nbut the lexer knows\n  %v", listed, known)
	}
	dirs, _ := os.ReadDir("../conformance")
	for _, d := range dirs {
		num := strings.TrimLeft(strings.SplitN(d.Name(), "-", 2)[0], "0")
		if !strings.Contains(spec, "\n## "+num+". ") {
			t.Errorf("conformance/%s has no section %s in docs/spec.md", d.Name(), num)
		}
	}
}

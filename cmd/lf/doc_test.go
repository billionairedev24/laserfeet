package main

import (
	"bytes"

	"path/filepath"
	"strings"
	"testing"
)

func TestDocFromComments(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.lf"), `-- shop: prices and baskets.

-- A thing for sale.
object Item {
    -- what it's called
    name: Text
    price: Decimal
    private cost: Decimal = 0

    -- the price with tax
    beam withTax(rate: Decimal) gives Decimal {
        send price + price * rate
    }
}

-- Where an order is.
choice Status { Open, Sent(on: Date) }

-- Adds up a basket.
beam total(items: List[Item]) gives Decimal {
    send 0
}

private beam secret() gives Int {
    send 1
}
`)
	df, err := describeFile(filepath.Join(dir, "package.lf"))
	if err != nil {
		t.Fatal(err)
	}
	md := docMarkdown([]docFile{df})
	for _, want := range []string{
		"shop: prices and baskets.",
		"## Item\n\n```\nobject Item\n```\n\nA thing for sale.",
		"- `name: Text`: what it's called",
		"- `beam withTax(rate: Decimal) gives Decimal`: the price with tax",
		"- `Sent(on: Date)`",
		"beam total(items: List[Item]) gives Decimal",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "secret") || strings.Contains(md, "cost") {
		t.Errorf("private beams and fields must be left out:\n%s", md)
	}
	page := docHTML([]docFile{df})
	if !strings.Contains(page, "<h2 id=\"Item\">Item</h2>") || strings.Contains(page, "<script") {
		t.Errorf("html page: %s", page)
	}

}

func TestTry(t *testing.T) {
	input := "x = 6\nx * 7\nbeam twice(n: Int) gives Int {\n    send n * 2\n}\ntwice(x)\n10 % 0\n\"{x} again\"\n"
	var out bytes.Buffer
	tryCommand(strings.NewReader(input), &out)
	got := out.String()
	for _, want := range []string{"42\n", "12\n", "you can't divide by zero", "6 again"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

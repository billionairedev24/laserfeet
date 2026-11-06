package lang

import (
	"strings"
	"testing"
)

func problemsIn(src string) string {
	var out []string
	for _, p := range Check(src, nil) {
		out = append(out, p.Error())
	}
	return strings.Join(out, "\n")
}

func TestCheckCatchesMistakes(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"wrong input type", "beam half(n: Int) gives Decimal {\n    send n / 2\n}\nshow half(\"ten\")",
			"line 4: the input 'n' must be Int, but this is Text"},
		{"can finish without sending", "beam sign(n: Int) gives Text {\n    when n > 0 {\n        send \"+\"\n    }\n}",
			"line 1: beam sign gives Text, but it can finish without sending one"},
		{"sends the wrong type", "beam name() gives Text {\n    send 42\n}",
			"line 2: what beam name sends must be Text, but this is Int"},
		{"sends without gives", "beam name() {\n    send \"Ada\"\n}",
			"line 2: beam name sends a value, so say what it gives, like: gives Text"},
		{"Float and Decimal", "x = float(1.5)\ny = 0.1 + 0.2\nshow x + y",
			"line 3: '+' can't mix a Float with an exact Decimal"},
		{"unknown field", "object Item {\n    price: Decimal\n}\ni = Item(2.5)\nshow i.prise",
			"line 5: Item has no field or beam called 'prise'. Did you mean 'price'?"},
		{"might be nothing", "beam find(n: Int) gives Text? {\n    send nothing\n}\nshow find(1).upper()",
			"line 4: this might be nothing here, so it has no 'upper'"},
		{"optional into non-optional", "beam find() gives Text? {\n    send nothing\n}\nbeam loud(t: Text) gives Text {\n    send t.upper()\n}\nshow loud(find())",
			"line 7: the input 't' must be Text, but this is Text? (it might be nothing)"},
		{"empty list needs a type", "names = []",
			"line 1: say what 'names' will hold, like: names: List[Text] = ..."},
		{"changing a value's type", "total = 0\ntotal = \"none\"",
			"line 2: 'total' must be Int, but this is Text"},
		{"locked", "lock speed = 100\nspeed = 200",
			"line 2: 'speed' is locked, so it can't be changed"},
		{"rule signature", "rule Shape {\n    beam area() gives Decimal\n}\nobject Box follows Shape {\n    side: Decimal\n    beam area() gives Text {\n        send \"big\"\n    }\n}",
			"the rule Shape says area is Beam() gives Decimal, but Box's area is Beam() gives Text"},
		{"unknown type", "x: Strng = \"hi\"",
			"there is no type called 'Strng'"},
		{"Java habit", "beam f(n: String) {\n}",
			"Laser Feet uses Text, Flag, Int, Float and Decimal"},
		{"wrong field type", "object Item {\n    price: Decimal\n}\nshow Item(\"free\")",
			"line 4: Item.price must be Decimal, but this is Text"},
		{"missing field", "object Item {\n    name: Text\n    price: Decimal\n}\nshow Item(name: \"Hat\")",
			"line 5: making a Item needs a value for 'price'"},
		{"list of the wrong things", "beam total(prices: List[Decimal]) gives Decimal {\n    send sum(prices)\n}\nshow total([\"a\"])",
			"line 4: an item of this list must be Decimal, but this is Text"},
		{"when needs yes or no", "count = 3\nwhen count {\n}",
			"line 2: 'when' needs a yes/no question, but this is Int"},
		{"unknown name", "name = \"Ada\"\nshow nmae",
			"line 2: I don't know what 'nmae' is. Did you mean 'name'?"},
		{"missing use", "server = web.open(8080)",
			"Add 'use web' at the top of the file"},
		{"misspelled route parameter", "use web\nserver = web.open(8080)\nserver.get(\"/u/:id\") {\n    send request.params.ids\n}",
			"line 4: the path /u/:id has no :ids, so request.params.ids is never set. Did you mean 'id'?"},
		{"findFirst may find nothing", "object U {\n    name: Text\n}\nus = [U(\"a\")]\nshow (us then findFirst(name is \"b\")).name",
			"line 5: this might be nothing here, so it has no 'name'"},
	}
	for _, c := range cases {
		got := problemsIn(c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s:\n  want a problem containing: %s\n  got: %s", c.name, c.want, got)
		}
	}
}

func TestCheckAcceptsGoodCode(t *testing.T) {
	cases := map[string]string{
		"nothing check narrows":          "beam find(n: Int) gives Text? {\n    send nothing\n}\nlaunch {\n    t = find(1)\n    when t is nothing {\n        show \"none\"\n        stop\n    }\n    show t.upper()\n}",
		"is not nothing narrows":         "beam find() gives Text? {\n    send nothing\n}\nt = find()\nwhen t is not nothing {\n    show t.upper()\n}",
		"Int widens to Decimal":          "beam price(p: Decimal) gives Decimal {\n    send p * 2\n}\nshow price(5)",
		"written decimal fits a Float":   "beam half(x: Float) gives Float {\n    send x / 2\n}\nshow half(2.5)",
		"typed empty list":               "names: List[Text] = []\nnames.add(\"Ada\")",
		"rules as types":                 "rule Named {\n    beam label() gives Text\n}\nobject Dog follows Named {\n    beam label() gives Text {\n        send \"dog\"\n    }\n}\nbeam say(x: Named) gives Text {\n    send x.label()\n}\nshow say(Dog())",
		"json is flexible":               "use json\ndata = json.read(\"{}\")\nshow data.anything.at.all",
		"every path sends":               "beam sign(n: Int) gives Text {\n    when n > 0 {\n        send \"+\"\n    } otherwise {\n        send \"-\"\n    }\n}",
		"decimal division":               "beam avg(a: Int, b: Int) gives Decimal {\n    send (a + b) / 2\n}",
		"optional field nothing default": "object P {\n    nick: Text? = nothing\n}\nshow P()",
		"web routes":                     "use web\nserver = web.open(8080)\nserver.get(\"/u/:id\") {\n    id = int(request.params.id)\n    send {id: id}\n}",
	}
	for name, src := range cases {
		if got := problemsIn(src); got != "" {
			t.Errorf("%s: expected no problems, got:\n%s", name, got)
		}
	}
}

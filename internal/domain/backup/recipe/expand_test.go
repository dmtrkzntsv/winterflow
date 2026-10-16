package recipe

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// plain is a node tree as Go values, read the way the x-winterflow parser
// walks it (an alias stays "*name", a merge key stays "<<"), so only an
// expanded tree reads like its expansion.
func plain(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		return plain(n.Content[0])
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = plain(n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		l := []any{}
		for _, c := range n.Content {
			l = append(l, plain(c))
		}
		return l
	case yaml.AliasNode:
		return "*" + n.Value
	}
	return n.Value
}

func TestDecodeFilesExpandsAliasesAndMergeKeys(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"an alias is its anchor's copy": {
			src:  "a: &x {k: 1}\nb: *x\n",
			want: "a: {k: 1}\nb: {k: 1}\n",
		},
		"the mapping's own keys win over merged ones": {
			src:  "base: &base {image: a, restart: always}\nsvc:\n  image: b\n  <<: *base\n",
			want: "base: {image: a, restart: always}\nsvc: {image: b, restart: always}\n",
		},
		"an earlier mapping of a merge list wins": {
			src:  "one: &one {a: 1, b: 1}\ntwo: &two {b: 2, c: 2}\nsvc:\n  <<: [*one, *two]\n",
			want: "one: {a: 1, b: 1}\ntwo: {b: 2, c: 2}\nsvc: {a: 1, b: 1, c: 2}\n",
		},
		"merges nest": {
			src:  "a: &a {x: 1}\nb: &b {<<: *a, y: 2}\nc: {<<: *b, z: 3}\n",
			want: "a: {x: 1}\nb: {x: 1, y: 2}\nc: {x: 1, y: 2, z: 3}\n",
		},
		"an inline mapping merges too": {
			src:  "svc: {<<: {a: 1}, b: 2}\n",
			want: "svc: {a: 1, b: 2}\n",
		},
		"a quoted << is an ordinary key": {
			src:  "a: &a {x: 1}\nsvc: {\"<<\": 5}\n",
			want: "a: {x: 1}\nsvc: {\"<<\": 5}\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var want yaml.Node
			if err := yaml.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			got := DecodeFiles([]byte(tc.src))[0]
			if got == nil {
				t.Fatal("refused")
			}
			if !reflect.DeepEqual(plain(got), plain(&want)) {
				t.Fatalf("got %#v\nwant %#v", plain(got), plain(&want))
			}
		})
	}
}

// The expanded tree shares no node between copies (a reader that edits one
// service's copy must not edit another's) and keeps the comments inside an
// anchor; no node keeps an anchor or an alias.
func TestDecodeFilesCopiesAnchors(t *testing.T) {
	src := "x-env: &env\n  # the greeting\n  GREETING: hi\nservices:\n  a: {environment: *env}\n  b:\n    environment:\n      <<: *env\n"
	doc := DecodeFiles([]byte(src))[0]
	if doc == nil {
		t.Fatal("refused")
	}
	svcs := doc.Content[0].Content[3]
	a, b := svcs.Content[1].Content[1], svcs.Content[3].Content[1]
	if a.Kind != yaml.MappingNode || b.Kind != yaml.MappingNode || len(a.Content) != 2 || len(b.Content) != 2 {
		t.Fatalf("environments: a = %+v, b = %+v", a, b)
	}
	if a.Content[0] == b.Content[0] || a.Content[0].HeadComment != "# the greeting" || b.Content[0].HeadComment != "# the greeting" {
		t.Fatalf("a = %+v, b = %+v", a.Content[0], b.Content[0])
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
			t.Fatalf("left in the tree: %+v", n)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
}

// A file whose aliases expand into themselves, past the cap, or merge
// something that is not a mapping reads as not valid YAML: nil, and
// Unparsed for the compose model.
func TestDecodeFilesRefusesWhatCantExpand(t *testing.T) {
	bomb := "a: &a [x, x, x, x, x, x, x, x, x, x]\n"
	for i, prev := 0, "a"; i < 6; i++ {
		name := fmt.Sprintf("l%d", i)
		bomb += name + ": &" + name + " [" + strings.Repeat("*"+prev+", ", 9) + "*" + prev + "]\n"
		prev = name
	}
	for name, src := range map[string]string{
		"a billion laughs":       bomb,
		"an alias in its anchor": "a: &a [*a]\n",
		"a merge in its anchor":  "a: &a {<<: *a}\n",
		"a merge of a scalar":    "a: &a 1\nb: {<<: *a}\n",
		"a merge list of lists":  "a: &a [1]\nb: {<<: [*a]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := DecodeFiles([]byte(src)); got[0] != nil {
				t.Fatal("expanded")
			}
			c, err := ParseCompose([]byte(src))
			if err != nil || !c.Unparsed {
				t.Fatalf("compose = %+v, %v", c, err)
			}
		})
	}
	// The cap counts every node of the expanded tree: the document, the
	// root, two keys, the anchored scalar and the list are 6, so a list of
	// MaxExpandedNodes-6 aliases is the most that expands.
	list := func(n int) []byte {
		return []byte("a: &a x\nk: [" + strings.TrimSuffix(strings.Repeat("*a, ", n), ", ") + "]\n")
	}
	if DecodeFiles(list(MaxExpandedNodes - 6))[0] == nil {
		t.Fatal("a tree of exactly MaxExpandedNodes was refused")
	}
	if DecodeFiles(list(MaxExpandedNodes - 5))[0] != nil {
		t.Fatal("a tree past MaxExpandedNodes expanded")
	}
}

// A service merged from an anchor carries its volumes: the backup recipe
// sees the mount.
func TestParseComposeSeesMergedServices(t *testing.T) {
	c := parse(t, "x-app: &app\n  image: example/app\n  volumes: [data:/data]\nservices:\n  web:\n    <<: *app\n  worker:\n    <<: *app\n    command: work\nvolumes: {data: {}}\n")
	if len(c.Mounts["web"]) != 1 || len(c.Mounts["worker"]) != 1 || c.Mounts["worker"][0] != c.Mounts["web"][0] {
		t.Fatalf("mounts = %+v", c.Mounts)
	}
}

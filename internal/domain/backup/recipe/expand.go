package recipe

import (
	"errors"

	yaml "go.yaml.in/yaml/v3"
)

// MaxExpandedNodes caps a compose file's YAML tree once its aliases and
// merge keys are expanded. A file past it reads as not valid YAML: a few
// lines of nested aliases (a "billion laughs") would otherwise expand into
// more nodes than the API, which parses org users' bodies and the catalog
// feed, has memory for.
const MaxExpandedNodes = 100_000

// errExpand marks a tree that can't be expanded: past MaxExpandedNodes,
// an alias inside its own anchor, or a merge key whose value is not a
// mapping or a list of mappings.
var errExpand = errors.New("yaml: aliases can't be expanded")

// expand returns a copy of doc with every alias replaced by a copy of its
// anchor and every merge key (<<: *anchor, or <<: [*a, *b]) replaced by the
// merged mappings' keys, as compose reads the file: a mapping's own keys win
// over merged ones, an earlier mapping of a merge list wins over a later
// one, and merges nest. Comments stay on the copied nodes (a variable's
// description can come from a comment inside an anchor); anchors are
// cleared, so no copy shares a node with another.
func expand(doc *yaml.Node) (*yaml.Node, error) {
	x := expander{active: map[*yaml.Node]bool{}}
	return x.node(doc)
}

type expander struct {
	nodes int
	// active are the nodes being copied: an alias to one of them is a cycle.
	active map[*yaml.Node]bool
}

func (x *expander) node(n *yaml.Node) (*yaml.Node, error) {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return nil, errExpand
		}
		return x.node(n.Alias)
	}
	if x.active[n] {
		return nil, errExpand
	}
	if x.nodes++; x.nodes > MaxExpandedNodes {
		return nil, errExpand
	}
	out := *n
	out.Anchor, out.Alias, out.Content = "", nil, nil
	if len(n.Content) == 0 {
		return &out, nil
	}
	x.active[n] = true
	defer delete(x.active, n)
	var err error
	if n.Kind == yaml.MappingNode {
		out.Content, err = x.mapping(n)
	} else {
		out.Content, err = x.list(n.Content)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (x *expander) list(in []*yaml.Node) ([]*yaml.Node, error) {
	out := make([]*yaml.Node, 0, len(in))
	for _, c := range in {
		cc, err := x.node(c)
		if err != nil {
			return nil, err
		}
		out = append(out, cc)
	}
	return out, nil
}

// mapping copies a mapping's pairs in order; each merge key is replaced, in
// place, by the merged mappings' pairs whose keys the mapping does not set
// itself and no earlier merged mapping set.
func (x *expander) mapping(n *yaml.Node) ([]*yaml.Node, error) {
	pairs, err := x.list(n.Content)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if !isMerge(n.Content[i]) && pairs[i].Kind == yaml.ScalarNode {
			taken[pairs[i].Value] = true
		}
	}
	out := make([]*yaml.Node, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		if !isMerge(n.Content[i]) {
			out = append(out, pairs[i], pairs[i+1])
			continue
		}
		sources := []*yaml.Node{pairs[i+1]}
		if pairs[i+1].Kind == yaml.SequenceNode {
			sources = pairs[i+1].Content
		}
		for _, src := range sources {
			if src.Kind != yaml.MappingNode {
				return nil, errExpand
			}
			for j := 0; j+1 < len(src.Content); j += 2 {
				k := src.Content[j]
				if k.Kind == yaml.ScalarNode {
					if taken[k.Value] {
						continue
					}
					taken[k.Value] = true
				}
				out = append(out, k, src.Content[j+1])
			}
		}
	}
	return out, nil
}

// isMerge reports whether k is a merge key: a plain <<, not a quoted "<<".
func isMerge(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Tag == "!!merge"
}

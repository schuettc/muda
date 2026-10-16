package compare

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// hostedRunnerLabel is the one list of runs-on values a runner-label-only
// change may swap between: GitHub-hosted runner labels such as ubuntu-latest,
// ubuntu-24.04, ubuntu-24.04-arm, macos-15 or windows-2025. It is narrow on
// purpose: a self-hosted runner, a runner group or an expression changes
// where the code and its secrets run, so any of those keeps the file
// UNVERIFIED.
var hostedRunnerLabel = regexp.MustCompile(`^(ubuntu|windows|macos)-(latest|[0-9]+(\.[0-9]+)?)(-[a-z0-9]+)*$`)

// treeSeg is one step from a document root: a mapping key or a sequence
// index.
type treeSeg struct {
	key     string
	index   int
	isIndex bool
}

// treeChange is one path where two YAML trees differ. A nil side means the
// path is absent there.
type treeChange struct {
	doc      int
	segs     []treeSeg
	old, new *yaml.Node
}

func (c treeChange) where() string {
	var b strings.Builder
	if c.doc > 0 {
		fmt.Fprintf(&b, "document[%d]", c.doc)
	}
	for _, s := range c.segs {
		if s.isIndex {
			fmt.Fprintf(&b, "[%d]", s.index)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.key)
	}
	return b.String()
}

// runnerLabelChanges compares two workflow files' YAML trees, comments
// ignored, and returns each jobs.<id>.runs-on change when those are the only
// differences and every old and new value is a plain hosted runner label.
// Anything else, including a file that does not decode, returns false.
func runnerLabelChanges(a, b []byte) ([]FieldChange, bool) {
	changes, err := treeChanges(a, b)
	if err != nil || len(changes) == 0 {
		return nil, false
	}
	out := []FieldChange{}
	for _, c := range changes {
		if c.doc != 0 || len(c.segs) != 3 || c.segs[0].isIndex || c.segs[1].isIndex || c.segs[2].isIndex ||
			c.segs[0].key != "jobs" || c.segs[2].key != "runs-on" || !hostedLabel(c.old) || !hostedLabel(c.new) {
			return nil, false
		}
		out = append(out, FieldChange{Where: c.where(), Old: value(c.old.Value), New: value(c.new.Value)})
	}
	return out, true
}

// hostedLabel is a single plain, untagged, unanchored string scalar that is a
// hosted runner label.
func hostedLabel(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Style == 0 && n.ShortTag() == "!!str" && n.Anchor == "" && hostedRunnerLabel.MatchString(n.Value)
}

// treeChanges decodes every YAML document of a and b, as commentFreeSHA256
// does, and lists the paths where the trees differ. It is strict: a node of a
// different kind, tag, anchor, alias, style or value, a sequence of another
// length, a key added, removed or reordered, or a mapping it cannot key by
// plain names is a difference.
func treeChanges(a, b []byte) ([]treeChange, error) {
	x, err := decodeDocs(a)
	if err != nil {
		return nil, err
	}
	y, err := decodeDocs(b)
	if err != nil {
		return nil, err
	}
	out := []treeChange{}
	if len(x) != len(y) {
		return append(out, treeChange{doc: -1}), nil
	}
	for i := range x {
		walkTrees(&out, treeChange{doc: i}, x[i], y[i])
	}
	return out, nil
}

func decodeDocs(data []byte) ([]*yaml.Node, error) {
	docs := []*yaml.Node{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, &doc)
	}
}

// sameNode compares one node's own content, not its children, positions or
// comments.
func sameNode(x, y *yaml.Node) bool {
	return x.Kind == y.Kind && x.Style == y.Style && x.Tag == y.Tag && x.Anchor == y.Anchor && x.Value == y.Value && len(x.Content) == len(y.Content)
}

func walkTrees(out *[]treeChange, at treeChange, x, y *yaml.Node) {
	diff := func(segs []treeSeg, old, new *yaml.Node) {
		*out = append(*out, treeChange{doc: at.doc, segs: segs, old: old, new: new})
	}
	child := func(s treeSeg) treeChange {
		return treeChange{doc: at.doc, segs: append(slices.Clone(at.segs), s)}
	}
	if x.Kind == yaml.MappingNode && y.Kind == yaml.MappingNode && sameMappingHeader(x, y) {
		walkMappings(out, at, x, y)
		return
	}
	if !sameNode(x, y) {
		diff(at.segs, x, y)
		return
	}
	switch x.Kind {
	case yaml.DocumentNode:
		for i := range x.Content {
			walkTrees(out, at, x.Content[i], y.Content[i])
		}
	case yaml.SequenceNode:
		for i := range x.Content {
			walkTrees(out, child(treeSeg{index: i, isIndex: true}), x.Content[i], y.Content[i])
		}
	case yaml.ScalarNode, yaml.AliasNode:
	default:
		// A kind this walk does not know is never called equal.
		diff(at.segs, x, y)
	}
}

func sameMappingHeader(x, y *yaml.Node) bool {
	return x.Style == y.Style && x.Tag == y.Tag && x.Anchor == y.Anchor && x.Value == y.Value
}

// mappingKeys returns a mapping's keys in order, or false when a key is not
// a scalar or appears twice.
func mappingKeys(n *yaml.Node) ([]string, map[string]int, bool) {
	if len(n.Content)%2 != 0 {
		return nil, nil, false
	}
	keys := []string{}
	at := map[string]int{}
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i]
		if k.Kind != yaml.ScalarNode {
			return nil, nil, false
		}
		if _, dup := at[k.Value]; dup {
			return nil, nil, false
		}
		at[k.Value] = i
		keys = append(keys, k.Value)
	}
	return keys, at, true
}

// walkMappings pairs two mappings' values by key. A key on one side only is a
// difference at that key; keys both sides share in another order, or with
// differently written key nodes, are a difference too.
func walkMappings(out *[]treeChange, at treeChange, x, y *yaml.Node) {
	xk, xi, ok := mappingKeys(x)
	yk, yi, ok2 := mappingKeys(y)
	if !ok || !ok2 {
		*out = append(*out, treeChange{doc: at.doc, segs: at.segs, old: x, new: y})
		return
	}
	child := func(k string) treeChange {
		return treeChange{doc: at.doc, segs: append(slices.Clone(at.segs), treeSeg{key: k})}
	}
	var xs, ys []string
	for _, k := range xk {
		if _, ok := yi[k]; !ok {
			c := child(k)
			c.old = x.Content[xi[k]+1]
			*out = append(*out, c)
			continue
		}
		xs = append(xs, k)
	}
	for _, k := range yk {
		if _, ok := xi[k]; !ok {
			c := child(k)
			c.new = y.Content[yi[k]+1]
			*out = append(*out, c)
			continue
		}
		ys = append(ys, k)
	}
	if !slices.Equal(xs, ys) {
		*out = append(*out, treeChange{doc: at.doc, segs: at.segs, old: x, new: y})
	}
	for _, k := range xs {
		if !sameNode(x.Content[xi[k]], y.Content[yi[k]]) {
			c := child(k)
			c.old, c.new = x.Content[xi[k]], y.Content[yi[k]]
			*out = append(*out, c)
			continue
		}
		walkTrees(out, child(k), x.Content[xi[k]+1], y.Content[yi[k]+1])
	}
}

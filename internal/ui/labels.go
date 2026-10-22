package ui

import (
	"image/color"
	"math"
	"sort"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// labelSeparator nests user labels the way Gmail does. The API exposes no
// parent field: the name "Work/Project A" is the label "Project A" below
// "Work", so the sidebar has to read the hierarchy back out of the string.
const labelSeparator = "/"

// maxLabelDepth caps the indentation. The sidebar column is narrow, so a
// deeper name would be left with no room for its text.
const maxLabelDepth = 3

// labelNode is one label of the sidebar together with the labels nested below
// it. The navItem is cached so a fold or unfold only re-flattens the tree and
// keeps the clickable state of every row.
type labelNode struct {
	item   *navItem
	id     string
	name   string
	parent *labelNode
	depth  int
	kids   []*labelNode
}

// labelTree is the parsed hierarchy of the user labels of one account.
type labelTree struct {
	roots []*labelNode
	byID  map[string]*labelNode
}

// labelChip is one label as a message row draws it: the full name, slash
// hierarchy included, and the colour of the chip background.
type labelChip struct {
	name string
	bg   color.NRGBA
}

// labelIndex resolves label ids to row chips. Only user labels enter the
// index, so the system ids a backend merges into a thread (INBOX, UNREAD,
// CATEGORY_*) can never be drawn as chips.
type labelIndex map[string]labelChip

// newLabelIndex builds the lookup of the user labels of an account.
func newLabelIndex(folders []mail.Folder) labelIndex {
	idx := make(labelIndex, len(folders))
	for _, f := range folders {
		if f.Kind != mail.FolderLabel || f.ID == "" || strings.TrimSpace(f.Name) == "" {
			continue
		}
		bg := colChipFallback
		if strings.TrimSpace(f.Color) != "" {
			bg = hexColor(f.Color)
		}
		idx[f.ID] = labelChip{name: f.Name, bg: bg}
	}
	return idx
}

// chips resolves the label ids of one conversation in backend order, skipping
// the label the list is currently filtered by as well as unknown and duplicate
// ids. A conversation without labels allocates nothing.
func (i labelIndex) chips(ids []string, skip string) []labelChip {
	if len(ids) == 0 {
		return nil
	}
	var out []labelChip
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || id == skip || seen[id] {
			continue
		}
		chip, ok := i[id]
		if !ok {
			continue
		}
		seen[id] = true
		out = append(out, chip)
	}
	return out
}

// chipTextColor picks the text colour with the better contrast on bg, so a
// pale label keeps dark text while a saturated one turns white, as Gmail does.
func chipTextColor(bg color.NRGBA) color.NRGBA {
	light := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	bgLum := relativeLuminance(bg)
	if contrastRatio(bgLum, relativeLuminance(light)) >= contrastRatio(bgLum, relativeLuminance(colText)) {
		return light
	}
	return colText
}

// relativeLuminance is the WCAG relative luminance of a colour.
func relativeLuminance(c color.NRGBA) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// contrastRatio is the WCAG contrast ratio of two relative luminances.
func contrastRatio(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// buildLabelTree groups the labels by the "/" in their names. System folders
// are ignored: only label rows can nest.
func buildLabelTree(folders []mail.Folder, onTap func(string)) *labelTree {
	t := &labelTree{byID: make(map[string]*labelNode, len(folders))}
	order := make([]string, 0, len(folders))
	paths := make(map[string][]string, len(folders))
	byPath := make(map[string]mail.Folder, len(folders))
	for _, f := range folders {
		if f.Kind != mail.FolderLabel {
			continue
		}
		parts := labelPath(f.Name)
		path := strings.Join(parts, labelSeparator)
		if _, ok := byPath[path]; ok {
			// Gmail label names are unique. A duplicate would draw two rows
			// for one label, so the first one wins.
			continue
		}
		byPath[path] = f
		paths[path] = parts
		order = append(order, path)
	}

	nodes := make(map[string]*labelNode, len(order))
	for _, path := range order {
		f := byPath[path]
		parts := paths[path]
		name := parts[len(parts)-1]
		item := newNavItemBar(f.ID, name, hexColor(f.Color), onTap)
		item.setCount(f.Unread)
		node := &labelNode{item: item, id: f.ID, name: name}
		nodes[path] = node
		t.byID[f.ID] = node
	}

	for _, path := range order {
		node := nodes[path]
		parent, ok := nodes[parentLabelPath(path)]
		if !ok {
			// Gmail normally creates the parent label, but an account can
			// still list "Work/Project A" without "Work". Such a row stays at
			// the top level under its full name instead of becoming a group
			// that cannot be selected.
			node.name = strings.Join(paths[path], labelSeparator)
			node.item.label = node.name
			t.roots = append(t.roots, node)
			continue
		}
		node.parent = parent
		parent.kids = append(parent.kids, node)
	}

	sortLabelNodes(t.roots)
	for _, root := range t.roots {
		walkLabelNodes(root, 0)
	}
	return t
}

// walkLabelNodes sorts every level and records the depth that sets the
// indentation of the row.
func walkLabelNodes(n *labelNode, depth int) {
	n.depth = depth
	n.item.depth = min(depth, maxLabelDepth)
	n.item.label = n.name
	sortLabelNodes(n.kids)
	for _, kid := range n.kids {
		walkLabelNodes(kid, depth+1)
	}
}

// sortLabelNodes orders a level by name, case-insensitively like Gmail.
func sortLabelNodes(nodes []*labelNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := strings.ToLower(nodes[i].name), strings.ToLower(nodes[j].name)
		if a != b {
			return a < b
		}
		return nodes[i].name < nodes[j].name
	})
}

// labelPath splits a label name into its path segments, dropping the spaces
// and empty segments that a hand-made name may carry.
func labelPath(name string) []string {
	parts := make([]string, 0, 2)
	for _, part := range strings.Split(name, labelSeparator) {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		// A name made of separators only ("/") carries no hierarchy. Keep it
		// as one segment so the row still shows the label it stands for.
		parts = append(parts, name)
	}
	return parts
}

func parentLabelPath(path string) string {
	i := strings.LastIndex(path, labelSeparator)
	if i < 0 {
		return ""
	}
	return path[:i]
}

// flatten returns the rows to draw, in order. A collapsed node keeps its own
// row and hides the labels below it. The ancestors of the active label are
// always open, so folding a parent can never hide the current selection.
func (t *labelTree) flatten(collapsed map[string]bool, activeID string, onToggle func(string)) []*navItem {
	open := t.activeAncestors(activeID)
	out := make([]*navItem, 0, len(t.byID))
	var walk func(n *labelNode)
	walk = func(n *labelNode) {
		expanded := !collapsed[n.id] || open[n.id]
		item := n.item
		item.expandable = len(n.kids) > 0
		item.expanded = expanded
		// An ancestor of the active label is held open: folding it would hide
		// the selection, so its marker is only an indicator, not a target.
		item.foldLocked = open[n.id]
		item.onToggle = nil
		if onToggle != nil {
			item.onToggle = func() { onToggle(n.id) }
		}
		item.setActive(n.id == activeID)
		out = append(out, item)
		if !expanded {
			return
		}
		for _, kid := range n.kids {
			walk(kid)
		}
	}
	for _, root := range t.roots {
		walk(root)
	}
	return out
}

// activeAncestors collects the ids of the strict ancestors of the active
// label. The label itself is left out: selecting a parent must still allow
// folding the labels below it.
func (t *labelTree) activeAncestors(id string) map[string]bool {
	open := make(map[string]bool, maxLabelDepth)
	node := t.byID[id]
	if node == nil {
		return open
	}
	for node = node.parent; node != nil; node = node.parent {
		open[node.id] = true
	}
	return open
}

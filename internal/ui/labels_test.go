package ui

import (
	"reflect"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

func labelFolder(id, name string) mail.Folder {
	return mail.Folder{ID: id, Name: name, Kind: mail.FolderLabel}
}

// flattenRows builds the tree and returns the visible rows in display order.
func flattenRows(folders []mail.Folder, collapsed map[string]bool, active string) []*navItem {
	tree := buildLabelTree(folders, nil)
	return tree.flatten(collapsed, active, nil)
}

func rowNames(items []*navItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.label)
	}
	return names
}

func TestLabelTreeNestsBySlash(t *testing.T) {
	// The API returns labels in no useful order: the parent can follow its
	// child. The tree has to sort a level and keep the child under its parent.
	items := flattenRows([]mail.Folder{
		labelFolder("b", "LableB"),
		labelFolder("a1", "LabelA/LabelA.1"),
		labelFolder("a", "LabelA"),
	}, nil, "")

	if want := []string{"LabelA", "LabelA.1", "LableB"}; !reflect.DeepEqual(rowNames(items), want) {
		t.Fatalf("rows = %v, want %v", rowNames(items), want)
	}
	if !items[0].expandable || !items[0].expanded {
		t.Fatalf("LabelA is not an open parent: expandable=%v expanded=%v",
			items[0].expandable, items[0].expanded)
	}
	if got := []int{items[0].depth, items[1].depth, items[2].depth}; !reflect.DeepEqual(got, []int{0, 1, 0}) {
		t.Fatalf("depths = %v, want [0 1 0]", got)
	}
	if items[1].expandable {
		t.Fatal("a leaf label must not draw a disclosure arrow")
	}
}

func TestLabelTreeFoldsSubtree(t *testing.T) {
	folders := []mail.Folder{
		labelFolder("a", "LabelA"),
		labelFolder("a1", "LabelA/LabelA.1"),
		labelFolder("b", "LableB"),
	}
	items := flattenRows(folders, map[string]bool{"a": true}, "")

	if want := []string{"LabelA", "LableB"}; !reflect.DeepEqual(rowNames(items), want) {
		t.Fatalf("rows = %v, want %v", rowNames(items), want)
	}
	if items[0].expanded {
		t.Fatal("a folded parent still reports itself open")
	}
}

func TestLabelTreeKeepsActiveAncestorsOpen(t *testing.T) {
	folders := []mail.Folder{
		labelFolder("a", "LabelA"),
		labelFolder("a1", "LabelA/LabelA.1"),
		labelFolder("b", "LableB"),
	}
	// The selection must stay visible even when its parent was folded.
	items := flattenRows(folders, map[string]bool{"a": true}, "a1")

	if want := []string{"LabelA", "LabelA.1", "LableB"}; !reflect.DeepEqual(rowNames(items), want) {
		t.Fatalf("rows = %v, want %v", rowNames(items), want)
	}
	if !items[0].expanded {
		t.Fatal("the opened ancestor does not report itself expanded")
	}
	if !items[1].active {
		t.Fatal("the active label is not marked active")
	}
}

func TestLabelTreeDegradesMissingParent(t *testing.T) {
	// Gmail normally creates the parent, but the list can still hold a nested
	// name without it. Such a row stays selectable at the top level.
	items := flattenRows([]mail.Folder{labelFolder("p", "Work/Project A")}, nil, "")

	if len(items) != 1 {
		t.Fatalf("rows = %d, want 1: %v", len(items), rowNames(items))
	}
	if got := items[0].label; got != "Work/Project A" {
		t.Fatalf("label = %q, want the full name", got)
	}
	if items[0].depth != 0 || items[0].expandable {
		t.Fatalf("degraded row has depth %d and expandable=%v", items[0].depth, items[0].expandable)
	}
}

func TestLabelTreeCleansSeparators(t *testing.T) {
	items := flattenRows([]mail.Folder{
		labelFolder("a", " Work "),
		labelFolder("a1", "Work / Project A "),
		labelFolder("a2", "Work//Project B"),
	}, nil, "")

	if want := []string{"Work", "Project A", "Project B"}; !reflect.DeepEqual(rowNames(items), want) {
		t.Fatalf("rows = %v, want %v", rowNames(items), want)
	}
	if got := []int{items[0].depth, items[1].depth, items[2].depth}; !reflect.DeepEqual(got, []int{0, 1, 1}) {
		t.Fatalf("depths = %v, want [0 1 1]", got)
	}
}

func TestLabelTreeSortsEachLevel(t *testing.T) {
	items := flattenRows([]mail.Folder{
		labelFolder("z", "zebra"),
		labelFolder("a", "Apple"),
		labelFolder("a2", "Apple/Seeds"),
		labelFolder("a1", "Apple/cores"),
	}, nil, "")

	if want := []string{"Apple", "cores", "Seeds", "zebra"}; !reflect.DeepEqual(rowNames(items), want) {
		t.Fatalf("rows = %v, want %v", rowNames(items), want)
	}
}

func TestLabelTreeSkipsSystemFolders(t *testing.T) {
	items := flattenRows([]mail.Folder{
		{ID: "inbox", Name: "Inbox", Kind: mail.FolderInbox},
		labelFolder("a", "Work"),
	}, nil, "")

	if want := []string{"Work"}; !reflect.DeepEqual(rowNames(items), want) {
		t.Fatalf("rows = %v, want %v", rowNames(items), want)
	}
}

func TestLabelTreeDropsDuplicateNames(t *testing.T) {
	items := flattenRows([]mail.Folder{
		labelFolder("first", "Work"),
		labelFolder("second", "Work"),
	}, nil, "")

	if len(items) != 1 {
		t.Fatalf("rows = %v, want one row for one name", rowNames(items))
	}
	if got := items[0].id; got != "first" {
		t.Fatalf("kept %q, want the first label", got)
	}
}

func TestLabelTreeCapsIndent(t *testing.T) {
	items := flattenRows([]mail.Folder{
		labelFolder("l1", "A"),
		labelFolder("l2", "A/B"),
		labelFolder("l3", "A/B/C"),
		labelFolder("l4", "A/B/C/D"),
		labelFolder("l5", "A/B/C/D/E"),
	}, nil, "")

	if len(items) != 5 {
		t.Fatalf("rows = %v, want five levels", rowNames(items))
	}
	if got := items[4].depth; got != maxLabelDepth {
		t.Fatalf("deepest row indents %d levels, want the cap %d", got, maxLabelDepth)
	}
	if got := items[4].label; got != "E" {
		t.Fatalf("deepest row shows %q, want its own name", got)
	}
}

func TestSidebarKeepsFoldsAcrossReload(t *testing.T) {
	a := testApp(t)
	a.folders = []mail.Folder{
		{ID: "inbox", Name: "Inbox", Kind: mail.FolderInbox},
		labelFolder("a", "Work"),
		labelFolder("a1", "Work/Project A"),
	}
	a.side.reload()
	if got := len(a.side.labels); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}

	a.side.toggleLabel("a")
	if got := len(a.side.labels); got != 1 {
		t.Fatalf("rows after the fold = %d, want 1", got)
	}

	// A refresh rebuilds the tree; the fold has to survive it.
	a.side.reload()
	if got := len(a.side.labels); got != 1 {
		t.Fatalf("rows after reload = %d, want the fold kept", got)
	}

	a.side.toggleLabel("a")
	if got := len(a.side.labels); got != 2 {
		t.Fatalf("rows after unfolding = %d, want 2", got)
	}
}

package ui

import (
	"slices"
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
)

// TestViewingKindFollowsTheActiveFolder pins the folder lookup the spam button
// depends on, including the search view (no folder at all) and the frame before
// the folder list arrives.
func TestViewingKindFollowsTheActiveFolder(t *testing.T) {
	folders := []mail.Folder{
		{ID: "inbox", Kind: mail.FolderInbox},
		{ID: "spam", Kind: mail.FolderSpam},
		{ID: "trash", Kind: mail.FolderTrash},
	}
	for _, tc := range []struct {
		name   string
		list   []mail.Folder
		active string
		spam   bool
		trash  bool
	}{
		{name: "inbox", list: folders, active: "inbox"},
		{name: "spam folder", list: folders, active: "spam", spam: true},
		{name: "trash folder", list: folders, active: "trash", trash: true},
		{name: "search view", list: folders, active: ""},
		{name: "before the folder list", active: "spam", spam: true},
		{name: "before the folder list, trash", active: "trash", trash: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.folders = tc.list
			a.activeFolder = tc.active
			if got := a.viewingSpam(); got != tc.spam {
				t.Fatalf("viewingSpam = %v, want %v", got, tc.spam)
			}
			if got := a.viewingTrash(); got != tc.trash {
				t.Fatalf("viewingTrash = %v, want %v", got, tc.trash)
			}
			if got := a.showsSpamAction(); got == tc.trash {
				t.Fatalf("showsSpamAction = %v in a trash folder = %v", got, tc.trash)
			}
		})
	}
}

// TestReaderSpamButtonReturnsToTheInbox covers the direction the trash folder
// implies: a conversation opened from spam goes back to the inbox, and the
// reader closes because the conversation left the folder on screen.
func TestReaderSpamButtonReturnsToTheInbox(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)
	a.folders = []mail.Folder{{ID: "spam", Kind: mail.FolderSpam}}
	a.activeFolder = "spam"
	a.selectedThread = "t1"
	a.thread = mail.ThreadSummary{ID: "t1", MessageIDs: []string{"m1", "m2"}}

	a.reader.spam.onTap()
	waitFor(t, a, "the spam change", func() bool { return len(svc.markCalls()) == 1 })

	got := svc.markCalls()[0]
	if got.spam {
		t.Fatal("the button in Spam filed the conversation as spam again")
	}
	if !slices.Equal(got.ids, []string{"m1", "m2"}) {
		t.Fatalf("ids = %v, want every message of the conversation", got.ids)
	}
	if a.thread.ID != "" {
		t.Fatalf("reader still shows %q after the conversation left Spam", a.thread.ID)
	}
}

// TestRowSpamButtonFilesTheThread covers the other direction: a row in the inbox
// files its own conversation as spam.
func TestRowSpamButtonFilesTheThread(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)
	a.folders = []mail.Folder{{ID: "inbox", Kind: mail.FolderInbox}}
	a.activeFolder = "inbox"
	a.list.setThreads([]mail.ThreadSummary{{ID: "t1", MessageIDs: []string{"m1"}}})

	row := newMessageRow(a.list)
	row.currentID = "t1"
	row.spam.onTap()
	waitFor(t, a, "the spam change", func() bool { return len(svc.markCalls()) == 1 })

	got := svc.markCalls()[0]
	if !got.spam {
		t.Fatal("the row button in the inbox cleared the spam mark instead of setting it")
	}
	if !slices.Equal(got.ids, []string{"m1"}) {
		t.Fatalf("ids = %v, want the row's messages", got.ids)
	}
}

// TestBulkSpamFollowsTheFolder pins that the bulk action reads the folder, not
// the rows: in Spam every selected conversation goes back to the inbox.
func TestBulkSpamFollowsTheFolder(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)
	a.folders = []mail.Folder{{ID: "spam", Kind: mail.FolderSpam}}
	a.activeFolder = "spam"
	a.list.setThreads([]mail.ThreadSummary{
		{ID: "t1", MessageIDs: []string{"m1"}},
		{ID: "t2", MessageIDs: []string{"m2", "m3"}},
	})
	a.list.selectAll()

	a.bulkToggleSpam()
	waitFor(t, a, "the spam change", func() bool { return len(svc.markCalls()) == 1 })

	got := svc.markCalls()[0]
	if got.spam {
		t.Fatal("the bulk button in Spam filed the conversations as spam again")
	}
	if !slices.Equal(got.ids, []string{"m1", "m2", "m3"}) {
		t.Fatalf("ids = %v, want every message of the selected conversations", got.ids)
	}
	if n := a.list.selectionCount(); n != 0 {
		t.Fatalf("%d rows are still selected after the action", n)
	}
}

// TestTrashLaysOutWithoutTheSpamButton keeps the frame path honest: the trash
// selects rows and opens a conversation, and neither view may draw a button the
// action would be a no-op in.
func TestTrashLaysOutWithoutTheSpamButton(t *testing.T) {
	a := testApp(t)
	a.folders = []mail.Folder{
		{ID: "inbox", Kind: mail.FolderInbox},
		{ID: "trash", Kind: mail.FolderTrash},
	}
	a.activeFolder = "trash"
	a.side.reload()
	threads := []mail.ThreadSummary{{ID: "t1", MessageIDs: []string{"m1"}, Subject: "gone"}}
	a.list.setThreads(threads)
	a.list.selected[threads[0].ID] = true
	a.selectedThread = "t1"
	a.thread = threads[0]
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m1", Subject: "gone"}}}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	a.layout(gtx)
	ops.Reset()

	if a.showsSpamAction() {
		t.Fatal("the trash offers a spam button")
	}
}

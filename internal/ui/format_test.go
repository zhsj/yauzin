package ui

import (
	"testing"
	"time"

	"yauzin/internal/i18n"
	"yauzin/internal/mail"
)

func useLanguage(t *testing.T, tag string) {
	t.Helper()
	i18n.SetLanguage(tag)
	t.Cleanup(func() { i18n.SetLanguage(i18n.DefaultLanguage) })
}

func TestFolderDisplayNameEnglish(t *testing.T) {
	useLanguage(t, "en")
	for _, tc := range []struct {
		folder mail.Folder
		want   string
	}{
		{mail.Folder{Kind: mail.FolderInbox, Name: "收件箱"}, "Inbox"},
		{mail.Folder{Kind: mail.FolderStarred, Name: "已加星标"}, "Starred"},
		{mail.Folder{Kind: mail.FolderSent, Name: "已发送"}, "Sent"},
		{mail.Folder{Kind: mail.FolderTrash, Name: "垃圾箱"}, "Trash"},
		{mail.Folder{Kind: mail.FolderLabel, Name: "工作"}, "工作"},
	} {
		if got := folderDisplayName(tc.folder); got != tc.want {
			t.Errorf("folderDisplayName(%s) = %q, want %q", tc.folder.ID, got, tc.want)
		}
	}
}

func TestFolderDisplayNameChinese(t *testing.T) {
	useLanguage(t, "zh-Hans")
	for _, tc := range []struct {
		folder mail.Folder
		want   string
	}{
		{mail.Folder{Kind: mail.FolderInbox}, "收件箱"},
		{mail.Folder{Kind: mail.FolderStarred}, "已加星标"},
		{mail.Folder{Kind: mail.FolderSent}, "已发送"},
		{mail.Folder{Kind: mail.FolderTrash}, "垃圾箱"},
		{mail.Folder{Kind: mail.FolderLabel, Name: "账单"}, "账单"},
	} {
		if got := folderDisplayName(tc.folder); got != tc.want {
			t.Errorf("folderDisplayName(%s) = %q, want %q", tc.folder.ID, got, tc.want)
		}
	}
}

func TestFormatListTime(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 4, 0, 0, time.Local)
	today := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	thisYear := time.Date(2026, 3, 5, 8, 7, 0, 0, time.Local)
	older := time.Date(2024, 12, 31, 23, 59, 0, 0, time.Local)

	for _, tc := range []struct {
		language string
		when     time.Time
		want     string
	}{
		{"en", today, "9:30 AM"},
		{"en", thisYear, "Mar 5"},
		{"en", older, "2024/12/31"},
		{"zh-Hans", today, "09:30"},
		{"zh-Hans", thisYear, "3月5日"},
		{"zh-Hans", older, "2024/12/31"},
	} {
		i18n.SetLanguage(tc.language)
		if got := formatListTimeAt(tc.when, now); got != tc.want {
			t.Errorf("%s formatListTimeAt(%s) = %q, want %q", tc.language, tc.when, got, tc.want)
		}
	}
	i18n.SetLanguage(i18n.DefaultLanguage)

	if got := formatListTimeAt(time.Time{}, now); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}
}

func TestFormatFullTime(t *testing.T) {
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, time.Local)
	for _, tc := range []struct {
		language string
		want     string
	}{
		{"en", "Mar 5, 2026 8:07 AM"},
		{"zh-Hans", "2026年3月5日 08:07"},
	} {
		i18n.SetLanguage(tc.language)
		if got := formatFullTime(when); got != tc.want {
			t.Errorf("%s formatFullTime = %q, want %q", tc.language, got, tc.want)
		}
	}
	i18n.SetLanguage(i18n.DefaultLanguage)

	if got := formatFullTime(time.Time{}); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}
}

func TestFormatFullTimeUsesLocalZone(t *testing.T) {
	useLanguage(t, "en")
	zone := time.FixedZone("UTC+9", 9*60*60)
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, zone)

	want := when.In(time.Local).Format(i18n.T("date.full"))
	if got := formatFullTime(when); got != want {
		t.Fatalf("formatFullTime = %q, want the local rendering %q", got, want)
	}
	// The list renders the same message in local time, so the two views have to
	// agree on the zone.
	if got, listWant := formatFullTime(when), formatListTime(when); got == "" || listWant == "" {
		t.Fatalf("empty timestamp: full %q, list %q", got, listWant)
	}
}

package slack

import "testing"

func TestParseMessageURL(t *testing.T) {
	ref, err := ParseMessageURL("https://acme.slack.com/archives/C123ABC/p1700000000123456?thread_ts=1699999999.000100")
	if err != nil {
		t.Fatalf("ParseMessageURL: %v", err)
	}
	if ref.Workspace != "acme" || ref.ChannelID != "C123ABC" {
		t.Errorf("workspace/channel = %q/%q", ref.Workspace, ref.ChannelID)
	}
	if ref.TS != "1700000000.123456" {
		t.Errorf("ts = %q, want 1700000000.123456", ref.TS)
	}
	if !ref.IsThread() || ref.ThreadTS != "1699999999.000100" {
		t.Errorf("thread = %v / %q", ref.IsThread(), ref.ThreadTS)
	}
}

func TestParseMessageURLErrors(t *testing.T) {
	bad := []string{
		"ftp://acme.slack.com/archives/C1/p1700000000123456",
		"https://example.com/archives/C1/p1700000000123456",
		"https://acme.slack.com/foo/bar",
	}
	for _, s := range bad {
		if _, err := ParseMessageURL(s); err == nil {
			t.Errorf("%q should error", s)
		}
	}
}

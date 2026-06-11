package viewer

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderMarkdown_Basic(t *testing.T) {
	out := string(RenderMarkdown([]byte("# Title\n\n**bold** and `code`\n\n- a\n- b")))
	for _, want := range []string{"<h1", "Title", "<strong>bold</strong>", "<code>code</code>", "<li>a</li>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestRenderMarkdown_SanitizesXSS(t *testing.T) {
	cases := []string{
		"<script>alert(1)</script>",
		"[x](javascript:alert(1))",
		"<img src=x onerror=alert(1)>",
		`<div onclick="evil()">x</div>`,
	}
	for _, in := range cases {
		out := string(RenderMarkdown([]byte(in)))
		for _, bad := range []string{"<script", "onerror=", "onclick=", "javascript:"} {
			if strings.Contains(out, bad) {
				t.Errorf("XSS not sanitized: input %q -> %q (contains %q)", in, out, bad)
			}
		}
	}
}

func TestRenderMarkdown_LinksOpenInNewTab(t *testing.T) {
	out := string(RenderMarkdown([]byte("[x](https://example.com)")))
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("absolute link should get target=_blank: %q", out)
	}
	if !strings.Contains(out, "noopener") {
		t.Errorf("target=_blank link should get rel=noopener: %q", out)
	}
	// javascript: は依然サニタイズされる（target 付与で穴を空けない）
	bad := string(RenderMarkdown([]byte("[x](javascript:alert(1))")))
	if strings.Contains(bad, "javascript:") {
		t.Errorf("javascript: must still be stripped: %q", bad)
	}
}

func TestHandler_RendersHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/viewer/render", strings.NewReader("# Hi"))
	Handler()(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<h1") {
		t.Fatalf("no h1: %s", rec.Body.String())
	}
}

package auth

import (
	"html"
	"strings"
)

// themed builds an email in BLASTA's own look: warm off-white, the orange bolt mark,
// "BLASTA by AWHADI" in the header, an orange button, and the same colours in dark
// mode for mail apps that follow the system setting. It returns the plain-text
// version too, for clients that do not show HTML.
//
// Everything user-supplied goes through html.EscapeString. The layout is tables
// with inline styles because that is what mail clients agree on.
func themed(title string, paragraphs []string, buttonLabel, buttonURL, footnote string) (text, htmlBody string) {
	var t strings.Builder
	t.WriteString("BLASTA by AWHADI\n\n" + title + "\n\n")
	for _, p := range paragraphs {
		t.WriteString(p + "\n\n")
	}
	if buttonURL != "" {
		t.WriteString(buttonLabel + ":\n" + buttonURL + "\n\n")
	}
	if footnote != "" {
		t.WriteString(footnote + "\n")
	}

	var h strings.Builder
	h.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark">
<title>` + html.EscapeString(title) + `</title>
<style>
@media (prefers-color-scheme: dark) {
  .bg { background:#0d1117 !important; } .card { background:#161b22 !important; border-color:#30363d !important; }
  .tx { color:#e6edf3 !important; } .dim { color:#7d8590 !important; }
  .btn { background:#ff9d78 !important; } .btn a { color:#1a1207 !important; } .rule { border-color:#30363d !important; }
}
</style></head>
<body class="bg" style="margin:0;padding:0;background:#faf9f7;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" class="bg" style="background:#faf9f7;"><tr><td align="center" style="padding:32px 16px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;">
<tr><td style="padding:0 4px 18px;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;">
  <table role="presentation" cellpadding="0" cellspacing="0"><tr>
    <td width="34" height="34" align="center" valign="middle" style="background:#e8663a;border-radius:9px;color:#1a1207;font-size:18px;line-height:34px;">&#9889;&#xFE0E;</td>
    <td style="padding-left:11px;vertical-align:baseline;"><span class="tx" style="font-size:22px;line-height:1;font-weight:700;letter-spacing:0.035em;color:#1f2328;vertical-align:baseline;">BLASTA</span><span class="dim" style="font-size:12px;line-height:1;color:#59636e;padding-left:7px;vertical-align:baseline;">by AWHADI</span></td>
  </tr></table>
</td></tr>
<tr><td class="card" style="background:#ffffff;border:1px solid #d0d7de;border-radius:12px;padding:30px 30px 26px;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;">
  <h1 class="tx" style="margin:0 0 14px;font-size:22px;line-height:1.25;font-weight:600;letter-spacing:-0.02em;color:#1f2328;">` + html.EscapeString(title) + `</h1>
`)
	for _, p := range paragraphs {
		if strings.HasPrefix(p, "  ") { // a code to read out or type: large, spaced, monospaced
			h.WriteString(`  <p class="tx" style="margin:6px 0 18px;padding:16px 12px;text-align:center;font-family:'SFMono-Regular',Menlo,Consolas,monospace;font-size:34px;font-weight:700;letter-spacing:8px;color:#e8663a;background:#faf9f7;border:1px solid #d0d7de;border-radius:10px;">` + html.EscapeString(strings.TrimSpace(p)) + `</p>
`)
			continue
		}
		h.WriteString(`  <p class="tx" style="margin:0 0 14px;font-size:15px;line-height:1.6;color:#1f2328;">` + html.EscapeString(p) + `</p>
`)
	}
	if buttonURL != "" {
		h.WriteString(`  <table role="presentation" cellpadding="0" cellspacing="0" style="margin:22px 0 8px;"><tr>
    <td class="btn" style="background:#e8663a;border-radius:9px;"><a href="` + html.EscapeString(buttonURL) + `" style="display:inline-block;padding:13px 24px;font-size:15px;font-weight:700;color:#1a1207;text-decoration:none;">` + html.EscapeString(buttonLabel) + `</a></td>
  </tr></table>
  <p class="dim" style="margin:14px 0 0;font-size:12.5px;line-height:1.5;color:#59636e;">Or paste this address into your browser:<br><span style="word-break:break-all;">` + html.EscapeString(buttonURL) + `</span></p>
`)
	}
	h.WriteString(`</td></tr>
<tr><td style="padding:18px 6px 0;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;">
  <p class="dim" style="margin:0;font-size:12.5px;line-height:1.5;color:#59636e;">` + html.EscapeString(footnote) + `</p>
</td></tr>
</table></td></tr></table></body></html>`)
	return t.String(), h.String()
}

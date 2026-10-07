package server

import (
	"strings"
	"testing"
)

func TestPeopleKeepTheirOwnCopiesOfBuiltInTemplates(t *testing.T) {
	srv, d := siteWithDB(t)
	if code, _, b := newVisitor(t, srv.URL).do("POST", "/api/auth/register", `{"email":"admin@example.test","name":"A","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register admin: %d %s", code, b)
	}
	pat := newVisitor(t, srv.URL)
	if code, _, b := pat.do("POST", "/api/auth/register", `{"email":"pat@example.test","name":"Pat","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	sam := newVisitor(t, srv.URL)
	if code, _, b := sam.do("POST", "/api/auth/register", `{"email":"sam@example.test","name":"Sam","password":"correct horse battery"}`); code != 201 {
		t.Fatalf("register: %d %s", code, b)
	}
	if code, _, _ := newVisitor(t, srv.URL).do("GET", "/api/my-templates", ""); code != 401 {
		t.Errorf("anonymous = %d, want 401", code)
	}

	// Add a copy of the WordPress template with the settings filled in.
	code, _, b := pat.do("POST", "/api/my-templates", `{"presetId":"wordpress","name":"Our shop (staging)","description":"the staging shop","values":{"url":"https://staging.shop.example.test","post":"hello-world","adminCookie":"ALLOWED-NOT","bogus":"x"}}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	id := idOf(t, b)
	if !strings.Contains(b, `"title":"WordPress"`) || !strings.Contains(b, `"category":"CMS"`) || !strings.Contains(b, `"jobs":24`) {
		t.Errorf("the row names the built-in template: %s", b)
	}
	// What is kept is sealed, and only the template's own typed-in settings (no credentials, no strangers).
	stored := dbRows(d, `SELECT data FROM user_presets`)
	if len(stored) != 1 || strings.Contains(stored[0], "staging.shop") {
		t.Errorf("the settings must be stored sealed: %v", stored)
	}
	_, _, g := pat.do("GET", "/api/my-templates/"+id, "")
	if !strings.Contains(g, "staging.shop.example.test") || strings.Contains(g, "ALLOWED-NOT") || strings.Contains(g, "bogus") {
		t.Errorf("get: %s", g)
	}
	if _, _, l := pat.do("GET", "/api/my-templates", ""); !strings.Contains(l, "Our shop (staging)") || strings.Contains(l, "staging.shop") {
		t.Errorf("the list shows names, not settings: %s", l)
	}

	// Private to the owner.
	for _, req := range [][3]string{{"GET", "/api/my-templates/" + id, ""}, {"PUT", "/api/my-templates/" + id, `{"name":"mine"}`}, {"DELETE", "/api/my-templates/" + id, ""}, {"POST", "/api/my-templates/" + id + "/duplicate", ""}} {
		if code, _, _ := sam.do(req[0], req[1], req[2]); code != 404 {
			t.Errorf("another person's %s %s = %d, want 404", req[0], req[1], code)
		}
	}

	// Change the settings, rename, duplicate.
	if code, _, u := pat.do("PUT", "/api/my-templates/"+id, `{"name":"Our shop","values":{"url":"https://shop2.example.test"}}`); code != 200 || !strings.Contains(u, `"name":"Our shop"`) {
		t.Errorf("update: %d %s", code, u)
	}
	if _, _, g = pat.do("GET", "/api/my-templates/"+id, ""); !strings.Contains(g, "shop2.example.test") || strings.Contains(g, "staging.shop") {
		t.Errorf("the new settings replace the old: %s", g)
	}
	if code, _, u := pat.do("PUT", "/api/my-templates/"+id, `{"description":"only the words changed"}`); code != 200 {
		t.Errorf("describe: %d %s", code, u)
	} else if _, _, g = pat.do("GET", "/api/my-templates/"+id, ""); !strings.Contains(g, "shop2.example.test") {
		t.Error("changing only the description must keep the settings")
	}
	if code, _, dup := pat.do("POST", "/api/my-templates/"+id+"/duplicate", ""); code != 201 || !strings.Contains(dup, "Our shop (copy)") {
		t.Errorf("duplicate: %d %s", code, dup)
	}

	// Bad input.
	for name, body := range map[string]string{"no such template": `{"presetId":"nope"}`, "empty": `{}`, "too long": `{"presetId":"wordpress","values":{"url":"` + strings.Repeat("a", 1001) + `"}}`} {
		if code, _, _ := pat.do("POST", "/api/my-templates", body); code != 400 {
			t.Errorf("%s = %d, want 400", name, code)
		}
	}
	// A name defaults to the template's own.
	if _, _, n := pat.do("POST", "/api/my-templates", `{"presetId":"redis"}`); !strings.Contains(n, `"name":"Redis / Valkey / KeyDB / Dragonfly"`) {
		t.Errorf("default name: %s", n)
	}
	// It is in the data download (with settings), and goes with the account.
	if _, _, me := pat.do("GET", "/api/me/export", ""); !strings.Contains(me, `"template":"wordpress"`) || !strings.Contains(me, "shop2.example.test") {
		t.Errorf("data download: %s", me)
	}
	if code, _, _ := pat.do("DELETE", "/api/my-templates/"+id, ""); code != 200 {
		t.Errorf("delete = %d", code)
	}
	if code, _, b := pat.do("DELETE", "/api/me", `{"password":"correct horse battery"}`); code != 200 {
		t.Fatalf("delete account: %d %s", code, b)
	}
	if rows := dbRows(d, `SELECT id FROM user_presets`); len(rows) != 0 {
		t.Errorf("deleting the account removes its templates: %v", rows)
	}
}

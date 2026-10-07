package server

import (
	"strings"
	"testing"
)

const tplJob = `{"name":"x","executor":"http","method":"POST","target":{"url":"https://api.example.test/v1/orders?token=URLSECRET"},"headers":{"Authorization":"Bearer SECRETVALUE123","Content-Type":"application/json","X-Api-Key":"KEYVALUE456"},"body":"{\"password\":\"BODYSECRET789\"}","concurrency":2,"rps":5,"duration":2000000000}`

func TestPeopleKeepTheirOwnTemplates(t *testing.T) {
	srv, d := siteWithDB(t)
	// The first account is the administrator; the people below are ordinary users.
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
	anon := newVisitor(t, srv.URL)
	if code, _, _ := anon.do("GET", "/api/my-templates", ""); code != 401 {
		t.Errorf("anonymous list = %d, want 401", code)
	}

	// Save one.
	code, _, b := pat.do("POST", "/api/my-templates", `{"name":"Orders API","description":"nightly check","job":`+tplJob+`}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	id := idOf(t, b)
	if !strings.Contains(b, `"summary":"POST https://api.example.test/v1/orders"`) || strings.Contains(b, "URLSECRET") {
		t.Errorf("the readable summary must not carry the query string: %s", b)
	}

	// What is stored is sealed: no secret appears in the database.
	stored := dbRows(d, `SELECT data FROM user_templates`)
	if len(stored) != 1 {
		t.Fatalf("expected one stored template, got %d", len(stored))
	}
	for _, row := range stored {
		for _, secret := range []string{"SECRETVALUE123", "KEYVALUE456", "BODYSECRET789", "URLSECRET"} {
			if strings.Contains(row, secret) {
				t.Errorf("%s is stored in clear text", secret)
			}
		}
	}

	// The owner can list it and load it back whole, secrets included (to run it).
	if code, _, l := pat.do("GET", "/api/my-templates", ""); code != 200 || !strings.Contains(l, "Orders API") || strings.Contains(l, "SECRETVALUE123") {
		t.Errorf("list: %d %s", code, l)
	}
	if code, _, g := pat.do("GET", "/api/my-templates/"+id, ""); code != 200 || !strings.Contains(g, "SECRETVALUE123") || !strings.Contains(g, "BODYSECRET789") {
		t.Errorf("get: %d %s", code, g)
	}

	// Nobody else can see, change, copy or delete it.
	for _, req := range [][3]string{{"GET", "/api/my-templates/" + id, ""}, {"PUT", "/api/my-templates/" + id, `{"name":"mine now"}`}, {"DELETE", "/api/my-templates/" + id, ""}, {"POST", "/api/my-templates/" + id + "/duplicate", ""}, {"GET", "/api/my-templates/" + id + "/export", ""}} {
		if code, _, _ := sam.do(req[0], req[1], req[2]); code != 404 {
			t.Errorf("another person's %s %s = %d, want 404", req[0], req[1], code)
		}
	}
	if _, _, l := sam.do("GET", "/api/my-templates", ""); strings.Contains(l, "Orders API") {
		t.Error("another person's list must not show it")
	}

	// Rename, replace the setup, duplicate.
	if code, _, u := pat.do("PUT", "/api/my-templates/"+id, `{"name":"Orders API v2","description":"changed"}`); code != 200 || !strings.Contains(u, "Orders API v2") {
		t.Errorf("rename: %d %s", code, u)
	}
	if _, _, g := pat.do("GET", "/api/my-templates/"+id, ""); !strings.Contains(g, "SECRETVALUE123") {
		t.Error("renaming must keep the saved setup")
	}
	if code, _, d := pat.do("POST", "/api/my-templates/"+id+"/duplicate", ""); code != 201 || !strings.Contains(d, "Orders API v2 (copy)") {
		t.Errorf("duplicate: %d %s", code, d)
	}

	// An exported file carries no credential values.
	code, hdr, ex := pat.do("GET", "/api/my-templates/"+id+"/export", "")
	if code != 200 || !strings.Contains(hdr.Get("Content-Disposition"), ".json") {
		t.Fatalf("export: %d", code)
	}
	for _, secret := range []string{"SECRETVALUE123", "KEYVALUE456"} {
		if strings.Contains(ex, secret) {
			t.Errorf("the exported file holds %s", secret)
		}
	}
	if !strings.Contains(ex, "application/json") || !strings.Contains(ex, "api.example.test") {
		t.Errorf("the exported file should keep the harmless parts: %s", ex)
	}
	// The data download holds them too, without credentials.
	if _, _, me := pat.do("GET", "/api/me/export", ""); !strings.Contains(me, "Orders API v2") || strings.Contains(me, "SECRETVALUE123") {
		t.Errorf("the data download: %s", me)
	}

	// Bad input.
	for name, body := range map[string]string{"no name": `{"name":"","job":` + tplJob + `}`, "no job": `{"name":"x"}`, "bad job": `{"name":"x","job":{"executor":"http"}}`, "long name": `{"name":"` + strings.Repeat("a", 200) + `","job":` + tplJob + `}`} {
		if code, _, _ := pat.do("POST", "/api/my-templates", body); code != 400 {
			t.Errorf("%s = %d, want 400", name, code)
		}
	}

	// Delete, and the account's deletion removes the rest.
	if code, _, _ := pat.do("DELETE", "/api/my-templates/"+id, ""); code != 200 {
		t.Errorf("delete = %d", code)
	}
	if code, _, _ := pat.do("GET", "/api/my-templates/"+id, ""); code != 404 {
		t.Errorf("after delete = %d, want 404", code)
	}
	if code, _, b := pat.do("DELETE", "/api/me", `{"password":"correct horse battery"}`); code != 200 {
		t.Fatalf("delete account: %d %s", code, b)
	}
	if rows := dbRows(d, `SELECT id FROM user_templates`); len(rows) != 0 {
		t.Errorf("deleting the account must remove its templates: %v", rows)
	}
}

func TestImportIsOpenToVisitorsAndOnlyReads(t *testing.T) {
	srv := siteWithSettings(t)
	anon := newVisitor(t, srv.URL)
	code, _, b := anon.do("POST", "/api/import", `{"text":"curl -X POST https://api.example.test/x -H 'X-K: v' -d 'a=1'"}`)
	if code != 200 || !strings.Contains(b, `"format":"curl"`) || !strings.Contains(b, `"method":"POST"`) || !strings.Contains(b, "https://api.example.test/x") {
		t.Errorf("import: %d %s", code, b)
	}
	if code, _, b := anon.do("POST", "/api/import", `{"text":"this is nothing"}`); code != 400 || !strings.Contains(b, "does not look like") {
		t.Errorf("a bad import should say why: %d %s", code, b)
	}
	if code, _, _ := anon.do("POST", "/api/import", `not json`); code != 400 {
		t.Errorf("bad body = %d", code)
	}
}

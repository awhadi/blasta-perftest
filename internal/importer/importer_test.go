package importer

import (
	"strings"
	"testing"
)

func one(t *testing.T, text string) Request {
	t.Helper()
	r, err := Parse(text)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if len(r.Requests) < 1 {
		t.Fatalf("no requests: %+v", r)
	}
	return r.Requests[0]
}

func TestCurlAsABrowserCopiesIt(t *testing.T) {
	// "Copy as cURL (bash)" from a browser: $'...' strings, line continuations, escapes.
	rq := one(t, "curl 'https://api.example.test/v1/orders?limit=5' \\\n  -H 'accept: application/json' \\\n  -H 'authorization: Bearer abc.def' \\\n  -H 'content-type: application/json' \\\n  --data-raw $'{\"note\":\"caf\\u00e9\",\"line\":\"a\\nb\"}' \\\n  --compressed")
	if rq.Method != "POST" || rq.URL != "https://api.example.test/v1/orders?limit=5" {
		t.Errorf("method/url: %s %s", rq.Method, rq.URL)
	}
	if rq.Headers["authorization"] != "Bearer abc.def" || rq.Headers["content-type"] != "application/json" {
		t.Errorf("headers: %v", rq.Headers)
	}
	if rq.Body != "{\"note\":\"café\",\"line\":\"a\nb\"}" {
		t.Errorf("body: %q", rq.Body)
	}
}

func TestCurlOptions(t *testing.T) {
	cases := map[string]struct{ method, url, body, header string }{
		`curl example.test`:                                        {"GET", "http://example.test", "", ""},
		`curl -I https://example.test/`:                            {"HEAD", "https://example.test/", "", ""},
		`curl -X DELETE "https://example.test/items/3"`:            {"DELETE", "https://example.test/items/3", "", ""},
		`curl -XPUT https://example.test/x -d 'a=1' -d 'b=2'`:      {"PUT", "https://example.test/x", "a=1&b=2", "Content-Type"},
		`curl -G https://example.test/s -d q=go -d page=2`:         {"GET", "https://example.test/s?q=go&page=2", "", ""},
		`curl -sSL --url https://example.test/p -A "my agent/1"`:   {"GET", "https://example.test/p", "", "User-Agent"},
		`curl -u me:secret https://example.test/`:                  {"GET", "https://example.test/", "", "Authorization"},
		`curl https://example.test/ --data-urlencode "q=a b&c"`:    {"POST", "https://example.test/", "q=a+b%26c", "Content-Type"},
		`$ curl --request=PATCH --header "X-A: 1" https://e.test/`: {"PATCH", "https://e.test/", "", "X-A"},
	}
	for cmd, want := range cases {
		rq := one(t, cmd)
		if rq.Method != want.method || rq.URL != want.url || rq.Body != want.body {
			t.Errorf("%s\n got %s %s %q\nwant %s %s %q", cmd, rq.Method, rq.URL, rq.Body, want.method, want.url, want.body)
		}
		if want.header != "" {
			if _, ok := rq.Headers[want.header]; !ok {
				t.Errorf("%s: missing header %s in %v", cmd, want.header, rq.Headers)
			}
		}
	}
	if rq := one(t, `curl https://example.test/ -d '{"a":1}'`); rq.Headers["Content-Type"] != "application/json" {
		t.Errorf("a JSON body should be sent as JSON: %v", rq.Headers)
	}
	if rq := one(t, `curl -u me:secret https://example.test/`); rq.Headers["Authorization"] != "Basic bWU6c2VjcmV0" {
		t.Errorf("basic auth: %v", rq.Headers)
	}
	for _, bad := range []string{"curl", "curl -H", "curl 'unterminated", "wget https://example.test"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if rq := one(t, "curl https://e.test/{{host}}/x -H 'X-K: {{key}}'"); len(rq.Notes) == 0 || !strings.Contains(rq.Notes[0], "host") || !strings.Contains(rq.Notes[0], "key") {
		t.Errorf("placeholders should be flagged: %v", rq.Notes)
	}
}

const harJSON = `{"log":{"entries":[
 {"request":{"method":"GET","url":"https://app.example.test/","headers":[{"name":"Host","value":"app.example.test"},{"name":"Accept","value":"text/html"}]},"response":{"content":{"mimeType":"text/html"}}},
 {"request":{"method":"GET","url":"https://app.example.test/static/app.js","headers":[]},"response":{"content":{"mimeType":"application/javascript"}}},
 {"request":{"method":"GET","url":"https://app.example.test/logo.png","headers":[]},"response":{"content":{"mimeType":"image/png"}}},
 {"request":{"method":"OPTIONS","url":"https://api.example.test/v1/login","headers":[]},"response":{"content":{"mimeType":""}}},
 {"request":{"method":"POST","url":"https://api.example.test/v1/login","headers":[{"name":":authority","value":"api.example.test"},{"name":"content-type","value":"application/json"},{"name":"sec-fetch-mode","value":"cors"},{"name":"Cookie","value":"s=1"},{"name":"Content-Length","value":"20"}],"postData":{"mimeType":"application/json","text":"{\"user\":\"a\"}"}},"response":{"content":{"mimeType":"application/json"}}},
 {"request":{"method":"POST","url":"https://api.example.test/v1/login","headers":[],"postData":{"mimeType":"application/json","text":"{\"user\":\"a\"}"}},"response":{"content":{"mimeType":"application/json"}}}
]}}`

func TestHARKeepsCallsAndDropsAssets(t *testing.T) {
	r, err := Parse(harJSON)
	if err != nil || r.Format != "har" {
		t.Fatalf("%v %s", err, r.Format)
	}
	if len(r.Requests) != 2 || r.Skipped != 4 {
		t.Fatalf("requests=%d skipped=%d: %+v", len(r.Requests), r.Skipped, r.Requests)
	}
	login := r.Requests[1]
	if login.Method != "POST" || login.Body != `{"user":"a"}` || login.Headers["content-type"] != "application/json" {
		t.Errorf("login: %+v", login)
	}
	for k := range login.Headers {
		if strings.HasPrefix(k, ":") || strings.HasPrefix(strings.ToLower(k), "sec-") || strings.EqualFold(k, "content-length") {
			t.Errorf("browser-only header kept: %s", k)
		}
	}
	if len(login.Notes) == 0 || !strings.Contains(login.Notes[0], "cookies") {
		t.Errorf("a recorded cookie should be flagged: %v", login.Notes)
	}
	if _, err := Parse(`{"log":{"entries":[{"request":{"method":"GET","url":"https://x.test/a.png"},"response":{"content":{"mimeType":"image/png"}}}]}}`); err == nil {
		t.Error("a HAR with only assets must say so")
	}
}

const postmanJSON = `{"info":{"name":"Shop","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
 "variable":[{"key":"base","value":"https://shop.example.test"}],
 "auth":{"type":"bearer","bearer":[{"key":"token","value":"{{token}}"}]},
 "item":[
  {"name":"Catalog","item":[
    {"name":"List products","request":{"method":"GET","header":[{"key":"Accept","value":"application/json"},{"key":"X-Off","value":"1","disabled":true}],"url":{"raw":"{{base}}/products?limit=10"}}},
    {"name":"Search","request":{"method":"POST","url":"{{base}}/search","body":{"mode":"urlencoded","urlencoded":[{"key":"q","value":"shoes"},{"key":"skip","value":"x","disabled":true}]}}}
  ]},
  {"name":"Cart","request":{"method":"POST","url":{"raw":"https://shop.example.test/cart"},"body":{"mode":"raw","raw":"{\"sku\":1}"}}}
 ]}`

func TestPostmanCollection(t *testing.T) {
	r, err := Parse(postmanJSON)
	if err != nil || r.Format != "postman" || len(r.Requests) != 3 {
		t.Fatalf("%v %s %d", err, r.Format, len(r.Requests))
	}
	list := r.Requests[0]
	if list.Name != "Catalog / List products" || list.URL != "https://shop.example.test/products?limit=10" || list.Headers["Accept"] != "application/json" {
		t.Errorf("list: %+v", list)
	}
	if _, off := list.Headers["X-Off"]; off {
		t.Error("a disabled header must be left out")
	}
	if list.Headers["Authorization"] != "Bearer {{token}}" || len(list.Notes) == 0 || !strings.Contains(list.Notes[0], "token") {
		t.Errorf("the collection's bearer token should be a flagged placeholder: %v %v", list.Headers, list.Notes)
	}
	if s := r.Requests[1]; s.Body != "q=shoes" || s.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Errorf("search: %+v", s)
	}
	if c := r.Requests[2]; c.Body != `{"sku":1}` {
		t.Errorf("cart: %+v", c)
	}
}

const openapiYAML = `
openapi: 3.0.3
info: {title: Pets, version: "1"}
servers:
  - url: https://{env}.pets.example.test/v2
    variables: {env: {default: api}}
components:
  schemas:
    Pet:
      type: object
      properties:
        id: {type: integer}
        name: {type: string, example: Rex}
        born: {type: string, format: date}
        tags: {type: array, items: {type: string}}
  parameters:
    Limit: {name: limit, in: query, required: true, schema: {type: integer, default: 20}}
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
paths:
  /pets:
    get:
      operationId: listPets
      parameters: [{$ref: '#/components/parameters/Limit'}]
    post:
      summary: Add a pet
      security: [{key: []}]
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Pet'}
  /pets/{petId}:
    get:
      parameters: [{name: petId, in: path, required: true, schema: {type: integer}}]
`

func TestOpenAPIYAMLWithRefs(t *testing.T) {
	r, err := Parse(openapiYAML)
	if err != nil || r.Format != "openapi" || len(r.Requests) != 3 {
		t.Fatalf("%v %s %+v", err, r.Format, r.Requests)
	}
	by := map[string]Request{}
	for _, q := range r.Requests {
		by[q.Name] = q
	}
	if l := by["listPets"]; l.URL != "https://api.pets.example.test/v2/pets?limit=20" || l.Method != "GET" {
		t.Errorf("listPets: %+v", l)
	}
	add := by["Add a pet"]
	if add.Method != "POST" || add.Headers["Content-Type"] != "application/json" || !strings.Contains(add.Body, `"name":"Rex"`) ||
		!strings.Contains(add.Body, `"born":"2024-01-01"`) || !strings.Contains(add.Body, `"tags":["string"]`) {
		t.Errorf("add: %+v", add)
	}
	if len(add.Notes) == 0 || !strings.Contains(add.Notes[0], "authentication") {
		t.Errorf("a secured operation should be flagged: %v", add.Notes)
	}
	if g := by["GET /pets/{petId}"]; g.URL != "https://api.pets.example.test/v2/pets/1" {
		t.Errorf("path parameter: %+v", g)
	}
}

func TestOpenAPIJSONAndSwagger2(t *testing.T) {
	r, err := Parse(`{"swagger":"2.0","host":"api.old.example.test","basePath":"/v1","schemes":["https"],"paths":{"/items":{"post":{"parameters":[{"in":"body","name":"b","schema":{"type":"object","properties":{"n":{"type":"integer"}}}}]}}}}`)
	if err != nil || len(r.Requests) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if q := r.Requests[0]; q.URL != "https://api.old.example.test/v1/items" || q.Body != `{"n":1}` {
		t.Errorf("swagger 2: %+v", q)
	}
}

func TestParseRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, bad := range []string{"", "   ", "hello world", `{"a":1}`, `[1,2]`, "just: yaml", strings.Repeat("x", MaxInput+1)} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%.30q must be refused", bad)
		}
	}
}

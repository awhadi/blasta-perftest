package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type obj = map[string]any

func asMap(v any) obj {
	switch m := v.(type) {
	case obj:
		return m
	case map[any]any:
		out := obj{}
		for k, x := range m {
			out[fmt.Sprint(k)] = x
		}
		return out
	}
	return nil
}

func asList(v any) []any { l, _ := v.([]any); return l }

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ref follows a local "#/components/..." reference.
func ref(doc obj, v any, depth int) any {
	m := asMap(v)
	if m == nil || depth > 8 {
		return v
	}
	r := str(m["$ref"])
	if !strings.HasPrefix(r, "#/") {
		return v
	}
	var cur any = doc
	for _, part := range strings.Split(r[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		cm := asMap(cur)
		if cm == nil {
			return v
		}
		cur = cm[part]
	}
	if cur == nil {
		return v
	}
	return ref(doc, cur, depth+1)
}

// example builds a sample value for a schema.
func example(doc obj, schema any, depth int) any {
	s := asMap(ref(doc, schema, 0))
	if s == nil || depth > 5 {
		return nil
	}
	if v, ok := s["example"]; ok {
		return v
	}
	if v, ok := s["default"]; ok {
		return v
	}
	if e := asList(s["enum"]); len(e) > 0 {
		return e[0]
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if l := asList(s[k]); len(l) > 0 {
			return example(doc, l[0], depth+1)
		}
	}
	if l := asList(s["allOf"]); len(l) > 0 {
		merged := obj{}
		for _, part := range l {
			if m, ok := example(doc, part, depth+1).(obj); ok {
				for k, v := range m {
					merged[k] = v
				}
			}
		}
		return merged
	}
	t := str(s["type"])
	if t == "" && asMap(s["properties"]) != nil {
		t = "object"
	}
	switch t {
	case "object":
		out := obj{}
		props := asMap(s["properties"])
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = example(doc, props[k], depth+1)
		}
		return out
	case "array":
		return []any{example(doc, s["items"], depth+1)}
	case "integer":
		return 1
	case "number":
		return 1.5
	case "boolean":
		return true
	case "string":
		switch str(s["format"]) {
		case "date-time":
			return "2024-01-01T00:00:00Z"
		case "date":
			return "2024-01-01"
		case "uuid":
			return "00000000-0000-0000-0000-000000000000"
		case "email":
			return "user@example.com"
		case "uri", "url":
			return "https://example.com"
		}
		return "string"
	}
	return nil
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// OpenAPI reads an OpenAPI 3 or Swagger 2 document, as JSON or YAML. Each operation becomes a
// request with example values filled in from the document.
func OpenAPI(data []byte) (Result, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Result{}, fmt.Errorf("this is not a valid OpenAPI document: %w", err)
	}
	doc := asMap(raw)
	if doc == nil || (doc["openapi"] == nil && doc["swagger"] == nil) {
		return Result{}, errors.New("this is not an OpenAPI or Swagger document")
	}
	base := ""
	if servers := asList(doc["servers"]); len(servers) > 0 {
		s := asMap(servers[0])
		base = str(s["url"])
		for name, v := range asMap(s["variables"]) {
			base = strings.ReplaceAll(base, "{"+name+"}", scalar(asMap(v)["default"]))
		}
	} else if h := str(doc["host"]); h != "" {
		scheme := "https"
		if sc := asList(doc["schemes"]); len(sc) > 0 {
			scheme = str(sc[0])
		}
		base = scheme + "://" + h + str(doc["basePath"])
	}
	res := Result{Format: "openapi"}
	if base == "" || !strings.Contains(base, "://") {
		res.Warnings = append(res.Warnings, "The document does not say where the API is: put its address in front of each path.")
		if base == "" {
			base = "https://api.example.com"
		} else {
			base = "https://api.example.com" + base
		}
	}
	base = strings.TrimRight(base, "/")
	secured := asMap(asMap(doc["components"])["securitySchemes"]) != nil || doc["securityDefinitions"] != nil
	paths := asMap(doc["paths"])
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)
	for _, p := range pathKeys {
		item := asMap(ref(doc, paths[p], 0))
		for _, m := range []string{"get", "post", "put", "patch", "delete", "head"} {
			op := asMap(item[m])
			if op == nil {
				continue
			}
			if len(res.Requests) >= MaxRequests {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Only the first %d operations are listed.", MaxRequests))
				return res, nil
			}
			method := strings.ToUpper(m)
			path := p
			query := url.Values{}
			headers := map[string]string{}
			var formBody url.Values
			var body string
			params := append(append([]any{}, asList(item["parameters"])...), asList(op["parameters"])...)
			for _, pr := range params {
				prm := asMap(ref(doc, pr, 0))
				if prm == nil {
					continue
				}
				val := ""
				switch {
				case prm["example"] != nil:
					val = scalar(prm["example"])
				case asMap(prm["schema"]) != nil:
					val = scalar(example(doc, prm["schema"], 0))
				default:
					val = scalar(example(doc, prm, 0))
				}
				name := str(prm["name"])
				switch str(prm["in"]) {
				case "path":
					if val == "" || val == "string" {
						val = "1"
					}
					path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(val))
				case "query":
					if b, _ := prm["required"].(bool); b {
						query.Set(name, val)
					}
				case "header":
					if b, _ := prm["required"].(bool); b && !strings.EqualFold(name, "authorization") {
						headers[name] = val
					}
				case "body":
					b, _ := json.Marshal(example(doc, prm["schema"], 0))
					body = string(b)
					headers["Content-Type"] = "application/json"
				case "formData":
					if formBody == nil {
						formBody = url.Values{}
					}
					formBody.Set(name, val)
				}
			}
			if formBody != nil {
				body = formBody.Encode()
				headers["Content-Type"] = "application/x-www-form-urlencoded"
			}
			if rb := asMap(ref(doc, op["requestBody"], 0)); rb != nil {
				content := asMap(rb["content"])
				ct := ""
				for _, c := range []string{"application/json", "application/x-www-form-urlencoded"} {
					if content[c] != nil {
						ct = c
						break
					}
				}
				if ct == "" {
					for c := range content {
						ct = c
						break
					}
				}
				if ct != "" {
					media := asMap(content[ct])
					var ex any
					switch {
					case media["example"] != nil:
						ex = media["example"]
					case asMap(media["examples"]) != nil:
						for _, e := range asMap(media["examples"]) {
							ex = asMap(ref(doc, e, 0))["value"]
							break
						}
					default:
						ex = example(doc, media["schema"], 0)
					}
					if strings.Contains(ct, "x-www-form-urlencoded") {
						vals := url.Values{}
						for k, v := range asMap(ex) {
							vals.Set(k, scalar(v))
						}
						body = vals.Encode()
					} else if s, ok := ex.(string); ok {
						body = s
					} else if ex != nil {
						b, _ := json.Marshal(ex)
						body = string(b)
					}
					if body != "" {
						headers["Content-Type"] = ct
					}
				}
			}
			full := base + path
			if len(query) > 0 {
				full += "?" + query.Encode()
			}
			var notes []string
			if op["security"] != nil || (secured && op["security"] == nil && doc["security"] != nil) {
				notes = append(notes, "This operation needs authentication: add the header it expects.")
			}
			if strings.Contains(full, "{") {
				notes = append(notes, "Fill in the placeholders in the address.")
			}
			label := str(op["operationId"])
			if label == "" {
				label = str(op["summary"])
			}
			if label == "" {
				label = method + " " + p
			}
			if len(headers) == 0 {
				headers = nil
			}
			res.Requests = append(res.Requests, Request{Name: label, Method: method, URL: full, Headers: headers, Body: body, Notes: notes})
		}
	}
	if len(res.Requests) == 0 {
		return res, errors.New("the document lists no operations")
	}
	return res, nil
}

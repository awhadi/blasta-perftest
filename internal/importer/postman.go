package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type pmItem struct {
	Name    string   `json:"name"`
	Items   []pmItem `json:"item"`
	Request *struct {
		Method string          `json:"method"`
		Header []pmKV          `json:"header"`
		URL    json.RawMessage `json:"url"`
		Body   *struct {
			Mode       string `json:"mode"`
			Raw        string `json:"raw"`
			URLEncoded []pmKV `json:"urlencoded"`
			FormData   []pmKV `json:"formdata"`
			GraphQL    *struct {
				Query     string `json:"query"`
				Variables string `json:"variables"`
			} `json:"graphql"`
		} `json:"body"`
		Auth *pmAuth `json:"auth"`
	} `json:"request"`
}

type pmKV struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type pmAuth struct {
	Type   string `json:"type"`
	Bearer []pmKV `json:"bearer"`
	Basic  []pmKV `json:"basic"`
	APIKey []pmKV `json:"apikey"`
}

func kv(list []pmKV, key string) string {
	for _, e := range list {
		if e.Key == key {
			return e.Value
		}
	}
	return ""
}

// Postman reads a Postman collection (v2.0 or v2.1).
func Postman(data []byte) (Result, error) {
	var c struct {
		Info struct {
			Name string `json:"name"`
		} `json:"info"`
		Item     []pmItem `json:"item"`
		Variable []pmKV   `json:"variable"`
		Auth     *pmAuth  `json:"auth"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Result{}, fmt.Errorf("this is not a valid Postman collection: %w", err)
	}
	if len(c.Item) == 0 {
		return Result{}, errors.New("the collection has no requests")
	}
	vars := map[string]string{}
	for _, v := range c.Variable {
		if v.Key != "" && v.Value != "" {
			vars[v.Key] = v.Value
		}
	}
	fill := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "{{"+k+"}}", v)
		}
		return s
	}
	res := Result{Format: "postman"}
	var walk func(items []pmItem, folder string)
	walk = func(items []pmItem, folder string) {
		for _, it := range items {
			if len(res.Requests) >= MaxRequests {
				return
			}
			if len(it.Items) > 0 {
				walk(it.Items, strings.TrimPrefix(folder+" / "+it.Name, " / "))
				continue
			}
			if it.Request == nil {
				continue
			}
			rq := it.Request
			rawURL := ""
			var asString string
			if json.Unmarshal(rq.URL, &asString) == nil {
				rawURL = asString
			} else {
				var obj struct {
					Raw string `json:"raw"`
				}
				_ = json.Unmarshal(rq.URL, &obj)
				rawURL = obj.Raw
			}
			rawURL = fill(rawURL)
			if rawURL == "" {
				res.Skipped++
				continue
			}
			if !strings.Contains(rawURL, "://") && !strings.HasPrefix(rawURL, "{{") {
				rawURL = "https://" + rawURL
			}
			method := strings.ToUpper(rq.Method)
			if method == "" {
				method = "GET"
			}
			headers := map[string]string{}
			for _, h := range rq.Header {
				if !h.Disabled && h.Key != "" {
					headers[h.Key] = fill(h.Value)
				}
			}
			auth := rq.Auth
			if auth == nil {
				auth = c.Auth
			}
			if auth != nil {
				switch auth.Type {
				case "bearer":
					headers["Authorization"] = "Bearer " + fill(kv(auth.Bearer, "token"))
				case "apikey":
					if k := kv(auth.APIKey, "key"); k != "" && kv(auth.APIKey, "in") != "query" {
						headers[k] = fill(kv(auth.APIKey, "value"))
					}
				case "basic":
					res.Warnings = append(res.Warnings, "\""+it.Name+"\" uses basic authentication: add its Authorization header yourself.")
				}
			}
			body := ""
			var notes []string
			if rq.Body != nil {
				switch rq.Body.Mode {
				case "raw":
					body = fill(rq.Body.Raw)
				case "urlencoded":
					vals := url.Values{}
					for _, e := range rq.Body.URLEncoded {
						if !e.Disabled {
							vals.Add(e.Key, fill(e.Value))
						}
					}
					body = vals.Encode()
					if _, ok := headers["Content-Type"]; !ok {
						headers["Content-Type"] = "application/x-www-form-urlencoded"
					}
				case "formdata":
					vals := url.Values{}
					for _, e := range rq.Body.FormData {
						if !e.Disabled {
							vals.Add(e.Key, fill(e.Value))
						}
					}
					body = vals.Encode()
					notes = append(notes, "This was a form with possible files: BLASTA sends the fields as text only.")
				case "graphql":
					if rq.Body.GraphQL != nil {
						b, _ := json.Marshal(map[string]any{"query": rq.Body.GraphQL.Query})
						body = string(b)
						if _, ok := headers["Content-Type"]; !ok {
							headers["Content-Type"] = "application/json"
						}
					}
				}
			}
			label := it.Name
			if folder != "" {
				label = folder + " / " + it.Name
			}
			if len(headers) == 0 {
				headers = nil
			}
			notes = append(notes, varNote(append(headerValues(headers), rawURL, body)...)...)
			res.Requests = append(res.Requests, Request{Name: label, Method: method, URL: rawURL, Headers: headers, Body: body, Notes: notes})
		}
	}
	walk(c.Item, "")
	if len(res.Requests) == 0 {
		return res, errors.New("the collection has no requests")
	}
	return res, nil
}

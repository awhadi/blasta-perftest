package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

type harFile struct {
	Log struct {
		Entries []struct {
			Request struct {
				Method   string                         `json:"method"`
				URL      string                         `json:"url"`
				Headers  []struct{ Name, Value string } `json:"headers"`
				PostData struct {
					MimeType string                         `json:"mimeType"`
					Text     string                         `json:"text"`
					Params   []struct{ Name, Value string } `json:"params"`
				} `json:"postData"`
			} `json:"request"`
			Response struct {
				Content struct {
					MimeType string `json:"mimeType"`
				} `json:"content"`
			} `json:"response"`
		} `json:"entries"`
	} `json:"log"`
}

var staticExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true, ".webp": true, ".avif": true,
	".css": true, ".js": true, ".mjs": true, ".map": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".mp4": true, ".webm": true, ".mp3": true, ".wav": true}

// dropHeader says which headers a browser adds on its own and are not worth keeping.
func dropHeader(n string) bool {
	l := strings.ToLower(n)
	switch l {
	case "host", "content-length", "connection", "accept-encoding", "upgrade-insecure-requests", "te", "keep-alive", "origin", "referer", "priority":
		return true
	}
	return strings.HasPrefix(l, ":") || strings.HasPrefix(l, "sec-")
}

// HAR reads a HAR file saved from a browser's network tab. Images, styles, scripts, fonts and
// preflight requests are left out: a load test wants the calls the page makes, not its assets.
func HAR(data []byte) (Result, error) {
	var h harFile
	if err := json.Unmarshal(data, &h); err != nil {
		return Result{}, fmt.Errorf("this is not a valid HAR file: %w", err)
	}
	if len(h.Log.Entries) == 0 {
		return Result{}, errors.New("the HAR file has no requests")
	}
	res := Result{Format: "har"}
	seen := map[string]bool{}
	for _, e := range h.Log.Entries {
		rq := e.Request
		method := strings.ToUpper(rq.Method)
		u, err := url.Parse(rq.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || method == "" {
			res.Skipped++
			continue
		}
		mt := strings.ToLower(e.Response.Content.MimeType)
		if method == "OPTIONS" || staticExt[strings.ToLower(path.Ext(u.Path))] ||
			strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "font/") || strings.HasPrefix(mt, "text/css") ||
			strings.Contains(mt, "javascript") || strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/") {
			res.Skipped++
			continue
		}
		headers := map[string]string{}
		for _, hd := range rq.Headers {
			if !dropHeader(hd.Name) {
				headers[hd.Name] = hd.Value
			}
		}
		body := rq.PostData.Text
		if body == "" && len(rq.PostData.Params) > 0 {
			vals := url.Values{}
			for _, p := range rq.PostData.Params {
				vals.Add(p.Name, p.Value)
			}
			body = vals.Encode()
		}
		key := method + " " + rq.URL + "\n" + body
		if seen[key] {
			res.Skipped++
			continue
		}
		seen[key] = true
		var notes []string
		if _, ok := headers["Cookie"]; ok {
			notes = append(notes, "Carries the cookies of the browser session it was recorded in: they may have expired.")
		}
		if strings.Contains(strings.ToLower(rq.PostData.MimeType), "multipart") {
			notes = append(notes, "This was a file upload: the body is not complete.")
		}
		if len(headers) == 0 {
			headers = nil
		}
		res.Requests = append(res.Requests, Request{Name: name(method, rq.URL), Method: method, URL: rq.URL, Headers: headers, Body: body, Notes: notes})
		if len(res.Requests) >= MaxRequests {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Only the first %d requests are listed.", MaxRequests))
			break
		}
	}
	if len(res.Requests) == 0 {
		return res, errors.New("the HAR file holds only images, styles, scripts and similar: there is no request to test")
	}
	return res, nil
}

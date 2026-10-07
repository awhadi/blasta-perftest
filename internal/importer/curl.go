package importer

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// shellWords splits a command line the way a POSIX shell would: single quotes, double quotes,
// backslashes, line continuations and $'...' strings (what browsers produce for "Copy as cURL").
func shellWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		case c == '\\':
			if i+1 < len(r) {
				i++
				if r[i] == '\n' || (r[i] == '\r' && i+1 < len(r) && r[i+1] == '\n') {
					if r[i] == '\r' {
						i++
					}
					continue // line continuation
				}
				cur.WriteRune(r[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			i++
			for i < len(r) && r[i] != '\'' {
				cur.WriteRune(r[i])
				i++
			}
			if i >= len(r) {
				return nil, errors.New("a quote was never closed")
			}
		case c == '$' && i+1 < len(r) && r[i+1] == '\'':
			inWord = true
			i += 2
			for i < len(r) && r[i] != '\'' {
				if r[i] == '\\' && i+1 < len(r) {
					i++
					switch r[i] {
					case 'n':
						cur.WriteByte('\n')
					case 't':
						cur.WriteByte('\t')
					case 'r':
						cur.WriteByte('\r')
					case '\\', '\'', '"':
						cur.WriteRune(r[i])
					case 'u', 'U':
						n := 4
						if r[i] == 'U' {
							n = 8
						}
						if end := i + 1 + n; end <= len(r) {
							if v, err := strconv.ParseUint(string(r[i+1:end]), 16, 32); err == nil && utf8.ValidRune(rune(v)) {
								cur.WriteRune(rune(v))
								i += n
								break
							}
						}
						cur.WriteRune(r[i])
					case 'x':
						if i+2 < len(r) {
							if v, err := strconv.ParseUint(string(r[i+1:i+3]), 16, 8); err == nil {
								cur.WriteByte(byte(v))
								i += 2
								break
							}
						}
						cur.WriteRune(r[i])
					default:
						cur.WriteRune('\\')
						cur.WriteRune(r[i])
					}
				} else {
					cur.WriteRune(r[i])
				}
				i++
			}
			if i >= len(r) {
				return nil, errors.New("a quote was never closed")
			}
		case c == '"':
			inWord = true
			i++
			for i < len(r) && r[i] != '"' {
				if r[i] == '\\' && i+1 < len(r) && strings.ContainsRune(`"\$`+"`", r[i+1]) {
					i++
				} else if r[i] == '\\' && i+1 < len(r) && r[i+1] == '\n' {
					i += 2
					continue
				}
				cur.WriteRune(r[i])
				i++
			}
			if i >= len(r) {
				return nil, errors.New("a quote was never closed")
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	flush()
	return words, nil
}

// Curl reads one curl command.
func Curl(text string) (Result, error) {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(t, "$ ")
	words, err := shellWords(t)
	if err != nil {
		return Result{}, fmt.Errorf("could not read the command: %w", err)
	}
	if len(words) == 0 || !strings.EqualFold(words[0], "curl") {
		return Result{}, errors.New("the command must start with curl")
	}
	var (
		method, rawURL string
		headers        = map[string]string{}
		data           []string
		getData        bool
		head           bool
		notes          []string
		user           string
	)
	// Options that take a value; the rest are flags.
	takes := map[string]bool{"-X": true, "--request": true, "-H": true, "--header": true, "-d": true, "--data": true, "--data-raw": true,
		"--data-binary": true, "--data-ascii": true, "--data-urlencode": true, "-u": true, "--user": true, "-A": true, "--user-agent": true,
		"-b": true, "--cookie": true, "-e": true, "--referer": true, "--url": true, "-F": true, "--form": true, "-o": true, "--output": true,
		"-m": true, "--max-time": true, "--connect-timeout": true, "--proxy": true, "-x": true, "--retry": true, "-w": true, "--write-out": true, "-T": true, "--upload-file": true}
	for i := 1; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") || w == "-" {
			if rawURL == "" {
				rawURL = w
			}
			continue
		}
		// "--opt=value" and a flag glued to its value ("-XPOST").
		val, hasVal := "", false
		if strings.HasPrefix(w, "--") {
			if k := strings.Index(w, "="); k > 0 {
				w, val, hasVal = w[:k], w[k+1:], true
			}
		} else if len(w) > 2 && takes[w[:2]] {
			w, val, hasVal = w[:2], w[2:], true
		} else if len(w) > 2 && !takes[w] {
			// A run of short flags such as -sSLk: the last one may take the next word.
			for _, c := range w[1 : len(w)-1] {
				switch c {
				case 'I':
					head = true
				case 'G':
					getData = true
				}
			}
			w = "-" + string(w[len(w)-1])
		}
		if takes[w] && !hasVal {
			if i+1 >= len(words) {
				return Result{}, fmt.Errorf("%s needs a value", w)
			}
			i++
			val = words[i]
		}
		switch w {
		case "-X", "--request":
			method = strings.ToUpper(val)
		case "-H", "--header":
			if k := strings.Index(val, ":"); k > 0 {
				headers[strings.TrimSpace(val[:k])] = strings.TrimSpace(val[k+1:])
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii":
			data = append(data, val)
		case "--data-urlencode":
			if k := strings.Index(val, "="); k >= 0 {
				data = append(data, val[:k+1]+url.QueryEscape(val[k+1:]))
			} else {
				data = append(data, url.QueryEscape(val))
			}
		case "-u", "--user":
			user = val
		case "-A", "--user-agent":
			headers["User-Agent"] = val
		case "-b", "--cookie":
			headers["Cookie"] = val
		case "-e", "--referer":
			headers["Referer"] = val
		case "--url":
			rawURL = val
		case "-I", "--head":
			head = true
		case "-G", "--get":
			getData = true
		case "-F", "--form":
			notes = append(notes, "This request uploads a form with files (-F). BLASTA sends the fields as text only: check the body.")
			data = append(data, val)
		case "-k", "--insecure":
			notes = append(notes, "The command skips certificate checks (-k): turn on \"Skip TLS verification\" under Advanced if the site needs it.")
		}
	}
	if rawURL == "" {
		return Result{}, errors.New("there is no address in the command")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	body := strings.Join(data, "&")
	switch {
	case getData && body != "":
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		rawURL += sep + body
		body = ""
		if method == "" {
			method = "GET"
		}
	case method == "" && head:
		method = "HEAD"
	case method == "" && body != "":
		method = "POST"
	case method == "":
		method = "GET"
	}
	if user != "" {
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(user))
	}
	hasCT := false
	for k := range headers {
		if strings.EqualFold(k, "content-type") {
			hasCT = true
		}
	}
	if body != "" && !hasCT {
		// curl itself would send a form; a body that is plainly JSON was meant as JSON.
		if t := strings.TrimSpace(body); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			headers["Content-Type"] = "application/json"
		} else {
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	req := Request{Name: name(method, rawURL), Method: method, URL: rawURL, Headers: headers, Body: body, Notes: notes}
	req.Notes = append(req.Notes, varNote(append(headerValues(headers), rawURL, body)...)...)
	return Result{Format: "curl", Requests: []Request{req}}, nil
}

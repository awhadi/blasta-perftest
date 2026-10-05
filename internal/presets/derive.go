package presets

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// derive computes a value that cannot be written as a static template, because
// it depends on other variables in a way plain substitution cannot express. The
// SAML requests are the case: they are an XML document that is deflated and
// base64-encoded, so the SP entity ID or ACS URL cannot simply be spliced in.
//
// Supported kinds:
//
//	saml-authnrequest-redirect          HTTP-Redirect binding (deflate+base64+urlencode)
//	saml-authnrequest-redirect-passive  same, IsPassive="true" (must not show a login page)
//	saml-authnrequest-redirect-force    same, ForceAuthn="true" (must re-authenticate)
//	saml-authnrequest-redirect-unknownsp  same, from an unregistered SP (error path)
//	saml-authnrequest-post              HTTP-POST binding (base64+urlencode, for a form body)
//	saml-logoutrequest-redirect         single-logout request, HTTP-Redirect binding
//
// Inputs read from vars: ssoUrl, sloUrl, acsUrl, spEntityId, nameId. The
// request carries a fresh random ID and the current time, so render it shortly
// before running: some IdPs reject a request whose IssueInstant is far in the
// past. The requests are unsigned.
func derive(kind string, vars map[string]string) (string, error) {
	id := "_blasta" + randHex(10)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	authn := func(extra string) string {
		return `<samlp:AuthnRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
			`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="` + id + `" Version="2.0" ` +
			`IssueInstant="` + now + `" Destination="` + esc(vars["ssoUrl"]) + `" ` +
			`ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" ` +
			`AssertionConsumerServiceURL="` + esc(vars["acsUrl"]) + `"` + extra + `>` +
			`<saml:Issuer>` + esc(vars["spEntityId"]) + `</saml:Issuer>` +
			`<samlp:NameIDPolicy Format="urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified" AllowCreate="true"/>` +
			`</samlp:AuthnRequest>`
	}
	logout := func() string {
		return `<samlp:LogoutRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
			`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="` + id + `" Version="2.0" ` +
			`IssueInstant="` + now + `" Destination="` + esc(vars["sloUrl"]) + `">` +
			`<saml:Issuer>` + esc(vars["spEntityId"]) + `</saml:Issuer>` +
			`<saml:NameID>` + esc(vars["nameId"]) + `</saml:NameID>` +
			`</samlp:LogoutRequest>`
	}

	switch kind {
	case "saml-authnrequest-redirect":
		return redirectBinding(authn(""))
	case "saml-authnrequest-redirect-passive":
		return redirectBinding(authn(` IsPassive="true"`))
	case "saml-authnrequest-redirect-force":
		return redirectBinding(authn(` ForceAuthn="true"`))
	case "saml-authnrequest-redirect-unknownsp":
		vars = copyVars(vars)
		vars["spEntityId"] = "https://unknown-sp.blasta.invalid/saml/metadata"
		vars["acsUrl"] = "https://unknown-sp.blasta.invalid/saml/acs"
		return redirectBinding(authn(""))
	case "saml-authnrequest-post":
		return url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(authn("")))), nil
	case "saml-logoutrequest-redirect":
		return redirectBinding(logout())
	}
	return "", fmt.Errorf("unknown derived kind %q", kind)
}

// redirectBinding applies the SAML HTTP-Redirect encoding: raw DEFLATE, then
// base64, then URL-encoding (SAML bindings spec, section 3.4.4.1).
func redirectBinding(xmlDoc string) (string, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write([]byte(xmlDoc)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return url.QueryEscape(base64.StdEncoding.EncodeToString(buf.Bytes())), nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.ToLower(hex.EncodeToString(b))
}

func copyVars(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

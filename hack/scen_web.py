# ======================================================================
# Mail servers, REST services and SOAP services.
# ======================================================================
MH = [var("host", "mail.example.com", "Mail server host name", placeholder=True)]
MAILNOTE = "No mail is sent: this reads the greeting only. Mail servers throttle and ban noisy clients (per-IP connection limits, postscreen, fail2ban), so run from a whitelisted address; a '421 too many connections' reply is counted as an error and shows the limit works. "
add("mail-servers", "Mail servers (SMTP / IMAP / POP3)", "Mail",
    "Greeting checks and connection capacity for SMTP, submission, IMAP and POP3, including the implicit-TLS ports.",
    "Postfix, Exim, Dovecot, Courier, Sendmail, Stalwart, Exchange",
    MH + [var("smtpPort", "25", "SMTP port"), var("submissionPort", "587", "Submission port"), var("smtpsPort", "465", "SMTPS port (implicit TLS)"),
          var("imapPort", "143", "IMAP port"), var("imapsPort", "993", "IMAPS port (implicit TLS)"),
          var("pop3Port", "110", "POP3 port"), var("pop3sPort", "995", "POP3S port (implicit TLS)")],
    [
        tcpjob("smtp-banner", "SMTP greeting (port 25)", MAILNOTE + "Checks for the 220 greeting: the cost of every inbound delivery attempt, and what spam bots hammer.",
               "read", {"url": "tcp://{{host}}:{{smtpPort}}"}, expect="220", concurrency=10, rps=20, timeout=T30),
        tcpjob("submission-banner", "submission greeting (port 587)", MAILNOTE + "Where authenticated users send mail. Usually has stricter limits than port 25.",
               "read", {"url": "tcp://{{host}}:{{submissionPort}}"}, expect="220", concurrency=10, rps=20, timeout=T30),
        tcpjob("smtps-accept", "SMTPS accept (port 465)", "Implicit TLS: the server waits for a ClientHello, so only the TCP accept is timed, not a greeting.",
               "read", {"url": "tcp://{{host}}:{{smtpsPort}}"}, concurrency=10, rps=20),
        tcpjob("imap-banner", "IMAP greeting (port 143)", MAILNOTE + "Checks for '* OK'. Mail clients open several IMAP connections each, so this multiplies with users.",
               "read", {"url": "tcp://{{host}}:{{imapPort}}"}, expect="* OK", concurrency=20, rps=50, timeout=T30),
        tcpjob("imaps-accept", "IMAPS accept (port 993)", "Implicit TLS accept only. Dovecot's process limit (process_limit, client_limit) shows here.",
               "read", {"url": "tcp://{{host}}:{{imapsPort}}"}, concurrency=20, rps=50),
        tcpjob("pop3-banner", "POP3 greeting (port 110)", MAILNOTE + "Checks for '+OK'.",
               "read", {"url": "tcp://{{host}}:{{pop3Port}}"}, expect="+OK", concurrency=10, rps=20, timeout=T30),
        tcpjob("pop3s-accept", "POP3S accept (port 995)", "Implicit TLS accept only.",
               "read", {"url": "tcp://{{host}}:{{pop3sPort}}"}, concurrency=10, rps=20),
        tcpjob("smtp-ramp", "inbound connection ramp (port 25)", MAILNOTE + "Ramps greeting checks to find the connection limit (smtpd_client_connection_count_limit, max_connections). Staging servers only.",
               "read", {"url": "tcp://{{host}}:{{smtpPort}}"}, expect="220", concurrency=100, rps=100, ramp=M5, duration=M5 + M2, timeout=T30, queueSize=5000, maxWorkers=300),
        tcpjob("imap-ramp", "IMAP connection ramp (port 143)", MAILNOTE + "Ramps connections to find where Dovecot's client_limit or the file descriptor limit is reached, e.g. a morning login peak.",
               "read", {"url": "tcp://{{host}}:{{imapPort}}"}, expect="* OK", concurrency=100, rps=200, ramp=M5, duration=M5 + M2, timeout=T30, queueSize=5000, maxWorkers=300),
    ])

add("mail-web", "Mail web services (webmail, autodiscover, MTA-STS, spam filter)", "Mail",
    "HTTP side of a mail system: Roundcube / SOGo, autodiscover and autoconfig, MTA-STS, rspamd and Mailpit.",
    "Roundcube, SOGo, Mailcow, Mailu, rspamd, Mailpit, MailHog, JMAP",
    [var("url", "https://mail.example.com", "Webmail base URL, no trailing slash", placeholder=True),
     var("domain", "example.com", "Your mail domain", placeholder=True),
     var("email", "user@example.com", "A mailbox address for the autoconfig query", placeholder=True),
     var("mtaSts", "https://mta-sts.example.com/.well-known/mta-sts.txt", "Full URL of your MTA-STS policy", placeholder=True)],
    [
        job("login-page", "webmail login page", "First page of every webmail session.", "read", {"url": "{{url}}/"}, concurrency=20, rps=30, expect=[200, 302], headers=HTML),
        job("session-redirect", "inbox without a session", "Protected page with no session: should redirect to login quickly. Redirects are not followed.",
            "read", {"url": "{{url}}/?_task=mail&_mbox=INBOX"}, concurrency=20, rps=50, expect=[302, 303, 200, 401], meta=NOFOLLOW, headers=HTML),
        job("roundcube-asset", "Roundcube static asset", "The main JavaScript file; large and loaded by every visitor.",
            "read", {"url": "{{url}}/program/js/app.min.js"}, concurrency=30, rps=100, expect=[200, 404]),
        job("sogo-login", "SOGo login page", "SOGo's sign-in page (if you run SOGo).", "read", {"url": "{{url}}/SOGo/"}, concurrency=10, rps=20, expect=[200, 302, 404], meta=NOFOLLOW, headers=HTML),
        job("autoconfig", "Thunderbird autoconfig", "Queried by every new mail client set-up.",
            "read", {"url": "{{url}}/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress={{email}}"}, concurrency=10, rps=20, expect=[200, 404]),
        job("autodiscover", "Outlook autodiscover (POST)", "Outlook and mobile clients poll this on a timer, so it is a constant background load on Exchange-style systems.",
            "read", {"url": "{{url}}/autodiscover/autodiscover.xml"}, method="POST",
            body="<?xml version=\"1.0\"?><Autodiscover xmlns=\"http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006\"><Request><EMailAddress>{{email}}</EMailAddress><AcceptableResponseSchema>http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a</AcceptableResponseSchema></Request></Autodiscover>",
            headers={"Content-Type": "text/xml"}, concurrency=10, rps=20, expect=[200, 401, 404]),
        job("mta-sts", "MTA-STS policy", "Fetched by every sending mail server that supports MTA-STS, and cached for the policy's max_age.",
            "read", {"url": "{{mtaSts}}"}, concurrency=10, rps=30, expect=[200]),
        job("jmap-session", "JMAP session resource", "Entry point for JMAP clients (Stalwart, Fastmail, Cyrus). Redirects are not followed.",
            "read", {"url": "{{url}}/.well-known/jmap"}, concurrency=10, rps=30, expect=[200, 301, 302, 401], meta=NOFOLLOW, headers=JSONH),
        job("rspamd-ping", "rspamd controller ping", "Spam filter controller liveness.", "read", {"url": "{{url}}/rspamd/ping"}, concurrency=5, rps=20, expect=[200, 404]),
        job("mailpit-info", "Mailpit info", "Test mail catcher API.", "read", {"url": "{{url}}/api/v1/info"}, concurrency=5, rps=20, expect=[200, 404], headers=JSONH),
        job("mailpit-messages", "Mailpit message list", "Lists captured messages: grows with the mailbox.", "read", {"url": "{{url}}/api/v1/messages?limit=50"}, concurrency=5, rps=10, expect=[200, 404], headers=JSONH),
        job("login-ramp", "login page ramp", "Ramps requests to the login page to find where the webmail front end (PHP-FPM) saturates.",
            "read", {"url": "{{url}}/"}, concurrency=100, rps=200, ramp=M5, duration=M5 + M2, expect=[200, 302], queueSize=5000, maxWorkers=300, headers=HTML),
    ])

# ---------------------------------------------------------------- REST services
LIST = "{{url}}{{resource}}"
ITEM = "{{url}}{{resource}}/{{id}}"
AUTHH = {"Authorization": "Bearer {{token}}"}
JSONBODY = {"Content-Type": "application/json", "Accept": "application/json"}
items100 = "[" + ",".join('{"name":"blasta item %d","status":"active"}' % i for i in range(100)) + "]"
big_json = '{"name":"blasta large","notes":"' + ("lorem ipsum " * 8000) + '"}'
multipart = ("--blastaboundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"blasta.txt\"\r\n"
             "Content-Type: text/plain\r\n\r\n" + ("blasta upload line\r\n" * 200) + "--blastaboundary--\r\n")
add("rest-patterns", "REST API patterns (any service)", "API",
    "Every common REST scenario: lists, pagination, filters, items, caching, writes, bulk, uploads, content negotiation, error handling and auth.",
    "Any JSON REST API",
    [var("url", "https://api.example.com", "API base URL, no trailing slash", placeholder=True),
     var("resource", "/api/v1/items", "A collection path"),
     var("id", "1", "ID of an existing item", placeholder=True),
     var("uploadPath", "/api/v1/uploads", "A file upload endpoint"),
     var("token", "${API_TOKEN}", "Bearer token. Read from API_TOKEN by the CLI.", sensitive=True)],
    [
        job("list", "list collection", "The most common call. If it is slow with no filters, look at default page size and missing indexes.",
            "read", {"url": LIST}, headers=dict(JSONH, **AUTHH), expect=[200]),
        job("pagination-first", "pagination: first page", "Cursor or offset page 1.", "read", {"url": LIST + "?limit=50&offset=0"}, headers=dict(JSONH, **AUTHH), expect=[200]),
        job("pagination-deep", "pagination: deep offset", "OFFSET 10000 forces the database to skip 10,000 rows per request, so latency grows with the page number. Compare with the first page; if it is slower, move to cursor (keyset) pagination.",
            "read", {"url": LIST + "?limit=50&offset=10000"}, headers=dict(JSONH, **AUTHH), expect=[200], concurrency=10, rps=10, timeout=T30),
        job("filter", "filtered list", "Filtering on a column without an index is a table scan: the first thing to check when a list endpoint gets slower as data grows.",
            "read", {"url": LIST + "?status=active&q=test"}, headers=dict(JSONH, **AUTHH), expect=[200], concurrency=10, rps=20),
        job("sort", "sorted list", "Sorting on an unindexed field sorts every matching row in memory.",
            "read", {"url": LIST + "?sort=-created_at&limit=50"}, headers=dict(JSONH, **AUTHH), expect=[200], concurrency=10, rps=20),
        job("sparse-fields", "sparse fields", "Asking for fewer fields should be cheaper. If it costs the same, the API loads everything anyway.",
            "read", {"url": LIST + "?fields=id,name&limit=50"}, headers=dict(JSONH, **AUTHH), expect=[200]),
        job("expand", "expanded relations", "?include= or ?expand= triggers extra queries per row (the N+1 problem). Latency that grows with limit gives it away.",
            "read", {"url": LIST + "?include=owner&limit=50"}, headers=dict(JSONH, **AUTHH), expect=[200, 400], concurrency=10, rps=15),
        job("item", "get one item", "Primary key lookup: should be the fastest call the API has.", "read", {"url": ITEM}, headers=dict(JSONH, **AUTHH), expect=[200]),
        job("item-not-found", "get a missing item", "404 path: must be as cheap as a hit, and must not leak stack traces.", "read", {"url": LIST + "/0"}, headers=dict(JSONH, **AUTHH), expect=[404]),
        job("head", "HEAD item", "Existence check without the body.", "read", {"url": ITEM}, method="HEAD", headers=AUTHH, expect=[200]),
        job("options", "OPTIONS (CORS preflight)", "Browsers send this before cross-origin writes; it should never reach the application code.",
            "read", {"url": LIST}, method="OPTIONS", expect=[200, 204], headers={"Origin": "https://app.example.com", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "authorization,content-type"}),
        job("etag-revalidation", "conditional GET (If-None-Match)", "Sends a deliberately stale ETag. A correct API answers 200 with the new body; a 304 on a wrong ETag would be a caching bug, so only 200 counts.",
            "read", {"url": ITEM}, headers=dict(JSONH, **dict(AUTHH, **{"If-None-Match": "\"blasta-stale-etag\""})), expect=[200]),
        job("create", "create an item (POST)", "Write path with validation and an insert. Creates a row per request, so clean up afterwards. Staging only.",
            "write", {"url": LIST}, method="POST", body='{"name":"blasta item","status":"active"}', headers=dict(JSONBODY, **AUTHH), expect=[200, 201], concurrency=10, rps=20),
        job("idempotent-create", "retried create (Idempotency-Key)", "Clients retry on timeout. With the same Idempotency-Key the API must create ONE item and replay the response. 409 is also acceptable.",
            "write", {"url": LIST}, method="POST", body='{"name":"blasta idempotent","status":"active"}',
            headers=dict(JSONBODY, **dict(AUTHH, **{"Idempotency-Key": "blasta-fixed-key"})), expect=[200, 201, 409], concurrency=10, rps=20),
        job("update", "replace an item (PUT)", "Full update of the item named by id. Staging only.", "write", {"url": ITEM}, method="PUT",
            body='{"name":"blasta updated","status":"active"}', headers=dict(JSONBODY, **AUTHH), expect=[200, 204], concurrency=10, rps=20),
        job("patch", "partial update (PATCH)", "Single-field update; row lock contention shows when many requests hit the same id. Staging only.",
            "write", {"url": ITEM}, method="PATCH", body='{"status":"active"}', headers=dict(JSONBODY, **AUTHH), expect=[200, 204], concurrency=10, rps=20),
        job("delete-missing", "delete a missing item", "DELETE on an id that does not exist must be a fast 404 (or 204 if idempotent). Never points at a real item.",
            "write", {"url": LIST + "/0"}, method="DELETE", headers=AUTHH, expect=[204, 404]),
        job("bulk-create", "bulk create (100 items)", "One request carrying 100 rows: tests transaction size, validation loops and the request body limit. Creates 100 rows per request. Staging only.",
            "write", {"url": LIST + "/bulk"}, method="POST", body=items100, headers=dict(JSONBODY, **AUTHH), expect=[200, 201, 202, 404], concurrency=5, rps=5, timeout=T30),
        job("large-body", "large JSON body (~100 KB)", "Body size limits and parsing cost. 413 (payload too large) is a correct answer.",
            "write", {"url": LIST}, method="POST", body=big_json, headers=dict(JSONBODY, **AUTHH), expect=[200, 201, 400, 413], concurrency=5, rps=5, timeout=T30),
        job("upload", "multipart file upload (~4 KB)", "File upload path: multipart parsing, temp files and storage. Run with a larger file in your own job to test disk and proxy limits.",
            "write", {"url": "{{url}}{{uploadPath}}"}, method="POST", body=multipart,
            headers=dict(AUTHH, **{"Content-Type": "multipart/form-data; boundary=blastaboundary"}), expect=[200, 201, 204], concurrency=5, rps=5, timeout=T30),
        job("wrong-content-type", "wrong Content-Type", "A JSON body sent as text/plain must be refused with 415 or 400, not parsed or crash.",
            "read", {"url": LIST}, method="POST", body='{"name":"x"}', headers=dict(AUTHH, **{"Content-Type": "text/plain"}), expect=[400, 415]),
        job("malformed-json", "malformed JSON body", "Invalid JSON must give a clean 400, quickly, with no stack trace.",
            "read", {"url": LIST}, method="POST", body='{"name":', headers=dict(JSONBODY, **AUTHH), expect=[400, 422]),
        job("method-not-allowed", "unsupported method", "DELETE on the collection must not wipe it: expect 404 or 405.",
            "read", {"url": LIST}, method="PUT", body="{}", headers=dict(JSONBODY, **AUTHH), expect=[400, 404, 405]),
        job("negotiate-xml", "content negotiation (Accept: XML)", "Asking for a format the API does not offer should be 406 or the default JSON, not an error.",
            "read", {"url": LIST}, headers=dict(AUTHH, **{"Accept": "application/xml"}), expect=[200, 406]),
        job("unauthorized", "no credentials", "Rejected requests should be cheaper than accepted ones.", "read", {"url": LIST}, headers=JSONH, expect=[401, 403]),
        job("openapi", "OpenAPI document", "Swagger/OpenAPI JSON, fetched by docs and generators; it can be megabytes and is often generated per request.",
            "read", {"url": "{{url}}/openapi.json"}, headers=JSONH, expect=[200, 404], concurrency=5, rps=10),
        job("health", "health endpoint", "Load balancer probe.", "read", {"url": "{{url}}/health"}, expect=[200, 404], concurrency=5, rps=30),
        job("read-ramp", "read capacity ramp", "Ramps list requests to find the rate where latency climbs.",
            "read", {"url": LIST + "?limit=20"}, headers=dict(JSONH, **AUTHH), expect=[200], concurrency=100, rps=300, ramp=M5, duration=M5 + M2, queueSize=5000, maxWorkers=300),
        job("mixed-read-soak", "read soak (10 min)", "Steady load that finds leaks, pool exhaustion and cache decay.",
            "read", {"url": ITEM}, headers=dict(JSONH, **AUTHH), expect=[200], concurrency=30, rps=50, duration=M10, queueSize=5000),
    ])

# ---------------------------------------------------------------- SOAP services
ENV11 = ('<?xml version="1.0" encoding="utf-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="{{namespace}}">'
         '<soapenv:Header/><soapenv:Body><ns:{{operation}}><ns:{{param}}>{{value}}</ns:{{param}}></ns:{{operation}}></soapenv:Body></soapenv:Envelope>')
ENV12 = ('<?xml version="1.0" encoding="utf-8"?><soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope" xmlns:ns="{{namespace}}">'
         '<soap:Header/><soap:Body><ns:{{operation}}><ns:{{param}}>{{value}}</ns:{{param}}></ns:{{operation}}></soap:Body></soap:Envelope>')
ENVSEC = ('<?xml version="1.0" encoding="utf-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="{{namespace}}" '
          'xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">'
          '<soapenv:Header><wsse:Security><wsse:UsernameToken><wsse:Username>${SOAP_USER}</wsse:Username>'
          '<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText">${SOAP_PASSWORD}</wsse:Password>'
          '</wsse:UsernameToken></wsse:Security></soapenv:Header><soapenv:Body><ns:{{operation}}><ns:{{param}}>{{value}}</ns:{{param}}></ns:{{operation}}></soapenv:Body></soapenv:Envelope>')
ENVBIG = ENV11.replace("{{value}}", "{{value}}" + ("lorem ipsum " * 8000))
XML11 = {"Content-Type": "text/xml; charset=utf-8", "SOAPAction": "\"{{soapAction}}\"", "Accept": "text/xml"}
FAULTNOTE = "A SOAP Fault is returned with HTTP 500, so a Fault counts as an error here. "
add("soap-services", "SOAP web services", "SOAP",
    "SOAP 1.1 and 1.2 calls, WSDL, faults, WS-Security, large messages and hardening checks.",
    "Any SOAP service (JAX-WS, WCF, Axis, Spring-WS, Zeep, PHP SoapServer)",
    [var("url", "https://api.example.com/soap/service", "Service endpoint URL", placeholder=True),
     var("soapAction", "http://example.com/ws/GetItem", "SOAPAction of the operation", placeholder=True),
     var("namespace", "http://example.com/ws", "Target namespace of the service", placeholder=True),
     var("operation", "GetItem", "Operation (request element) name", placeholder=True),
     var("param", "id", "Name of one request parameter", placeholder=True),
     var("value", "1", "Its value")],
    [
        job("wsdl", "fetch the WSDL", "Clients fetch the WSDL at start-up, and some on every call (a common performance bug). It is often generated on the fly.",
            "read", {"url": "{{url}}?wsdl"}, headers={"Accept": "text/xml"}, expect=[200], concurrency=5, rps=10),
        job("wsdl-nocache", "fetch the WSDL (cache bypass)", "Forces regeneration.", "read", {"url": "{{url}}?wsdl"}, headers=dict(NOCACHE, Accept="text/xml"), expect=[200], concurrency=3, rps=5),
        job("soap11", "SOAP 1.1 call", FAULTNOTE + "The baseline request: XML parse, dispatch, business logic and response serialisation.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV11, headers=XML11, expect=[200], concurrency=20, rps=50),
        job("soap12", "SOAP 1.2 call", FAULTNOTE + "Same operation with the SOAP 1.2 envelope and content type; some stacks route the versions differently.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV12,
            headers={"Content-Type": "application/soap+xml; charset=utf-8; action=\"{{soapAction}}\"", "Accept": "application/soap+xml"}, expect=[200], concurrency=20, rps=50),
        job("ws-security", "WS-Security UsernameToken", FAULTNOTE + "Adds authentication in the header. Needs SOAP_USER and SOAP_PASSWORD (CLI only; the web UI does not expand environment variables). Token validation is often the slow part.",
            "read", {"url": "{{url}}"}, method="POST", body=ENVSEC, headers=XML11, expect=[200], concurrency=10, rps=30),
        job("fault-malformed", "malformed XML", "Not XML at all. The service must answer with a 400 or a SOAP Fault (HTTP 500) quickly, with no stack trace. Both count as handled.",
            "read", {"url": "{{url}}"}, method="POST", body="<not-xml", headers=XML11, expect=[400, 500], concurrency=10, rps=30),
        job("fault-unknown-operation", "unknown operation", "A valid envelope calling an operation that does not exist: a Fault is expected.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV11.replace("ns:{{operation}}", "ns:BlastaNoSuchOperation"), headers=XML11, expect=[400, 404, 500], concurrency=10, rps=30),
        job("wrong-soapaction", "wrong SOAPAction", "Header does not match the body. Strict services reject it; lenient ones ignore it. Either is fine, but it should not crash.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV11, headers=dict(XML11, SOAPAction="\"http://blasta.invalid/NoSuchAction\""), expect=[200, 400, 404, 500], concurrency=10, rps=30),
        job("empty-body", "empty request", "POST with no body.", "read", {"url": "{{url}}"}, method="POST", body="", headers=XML11, expect=[400, 500, 405], concurrency=10, rps=30),
        job("wrong-content-type", "wrong Content-Type", "XML sent as application/json: expect 415 or 400.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV11, headers={"Content-Type": "application/json", "SOAPAction": "\"{{soapAction}}\""}, expect=[400, 415, 500], concurrency=10, rps=30),
        job("dtd-rejected", "DTD in the request (XXE hardening)", "A harmless inline entity declaration. A hardened parser REFUSES documents with a DOCTYPE (400 or a Fault); a 200 here means DTDs are processed, which is an XML external entity risk.",
            "read", {"url": "{{url}}"}, method="POST",
            body=ENV11.replace('<soapenv:Header/>', '<soapenv:Header/>').replace('<?xml version="1.0" encoding="utf-8"?>', '<?xml version="1.0" encoding="utf-8"?><!DOCTYPE blasta [<!ENTITY e "blasta">]>').replace("{{value}}", "&e;"),
            headers=XML11, expect=[400, 500], concurrency=5, rps=10),
        job("large-message", "large message (~100 KB)", "Big requests stress the XML parser (DOM parsers hold the whole tree in memory) and body limits. A 413 or a Fault for too-large input is acceptable.",
            "read", {"url": "{{url}}"}, method="POST", body=ENVBIG, headers=XML11, expect=[200, 413, 500], concurrency=5, rps=5, timeout=T30),
        job("soap-ramp", "capacity ramp", FAULTNOTE + "Ramps the SOAP call to find where latency climbs. XML processing is CPU heavy, so expect a lower ceiling than for JSON.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV11, headers=XML11, expect=[200], concurrency=100, rps=200, ramp=M5, duration=M5 + M2, queueSize=5000, maxWorkers=300),
        job("soap-soak", "soak (10 min)", FAULTNOTE + "Steady load that finds leaks in session state, JAXB/DOM caches and connection pools.",
            "read", {"url": "{{url}}"}, method="POST", body=ENV11, headers=XML11, expect=[200], concurrency=30, rps=30, duration=M10, queueSize=5000),
    ])

# ======================================================================
# Guidance shown in the "Set up this job" dialog: what each value is and how to
# get it. Runs last. Fails the build if any variable or credential has no guide,
# so a new template cannot ship without explaining what it needs.
# ======================================================================
import re as _re

STAGING = " Use a system you own, ideally a staging copy."
HOSTDOCKER = " BLASTA runs in a container, so for a service on your own machine use host.docker.internal instead of localhost."

# ---- non-secret values, by variable name -------------------------------------
VAR_GUIDES = {
    "url": "The address of the system to test, starting with https:// (or http://) and with no trailing slash, for example https://staging.example.com." + STAGING + HOSTDOCKER,
    "wsUrl": "The WebSocket address, starting with wss:// (or ws://), for example wss://staging.example.com.",
    "host": "The host name or IP address of the server, without a port or http://, for example redis.staging.example.com." + HOSTDOCKER,
    "port": "The port the service listens on. The default is filled in; check the service's configuration if you changed it.",
    "target": "host:port of the service, for example staging.example.com:50051." + HOSTDOCKER,
    "path": "The part of the address after the domain, starting with /, for example /pricing or /api/health.\nPick a page that is typical of real traffic: the home page is often cached and flatters the result.",
    "post": "Open any published post on your site. The slug is the last part of its address: https://yoursite.com/<slug>/.\nIn the admin: Posts → All Posts → hover a title → the slug is in Quick Edit.",
    "article": "The numeric ID of an article. In Joomla's admin go to Content → Articles and read the ID column, or open the article and look for id=<number> in its address.",
    "node": "The number of a published page. Open it on the site: the address is /node/<number> (hover its title in Content in the admin if the site uses friendly URLs).",
    "topic": "A topic to read. Discourse: open a topic, the address is /t/<slug>/<id> and the job uses the slug and id. phpBB: open a topic and use t=<number> from the address.",
    "tag": "The slug of a tag that has posts. In Ghost admin open Tags, click one, and copy the slug from its settings.",
    "category": "The path or ID of a category that has products, as it appears in the address when you open that category on the shop.",
    "collection": "The API name of a collection. Strapi: Content-Type Builder → the collection's API ID (plural), for example articles. REST: the resource name in the API address.",
    "page": "A page that exists. MediaWiki: a page title, as in /wiki/<title>. TYPO3: a page ID. Static site: the path of an inner page, for example /about/.",
    "product": "The slug of an existing product. Open a product on the shop; the slug is the last part of its address.",
    "route": "A server-rendered route in your Next.js app, for example /products/1 or /blog/hello.",
    "course": "The numeric ID of a course. Open a course in Moodle: the address contains id=<number>.",
    "id": "The ID of an existing record, as it appears in the API or the address, for example 1.",
    "resource": "The path of a collection in the API, starting with /, for example /api/v1/items. Look in the API documentation or its OpenAPI file.",
    "endpoint": "An API path that requires authentication, starting with /, for example /api/v1/me.",
    "field": "A field that exists in your GraphQL schema, for example id or name. Open the schema or run an introspection query to list them.",
    "index": "The name of an index. Elasticsearch/OpenSearch: `GET /_cat/indices` lists them. Meilisearch: `GET /indexes`.",
    "term": "A word that appears in your data, so the search returns results.",
    "table": "A table that exists in your database, for example orders. Use a throwaway table for write jobs.",
    "prefix": "The WordPress table prefix, in wp-config.php as $table_prefix. The default is wp_.",
    "master": "The name of the monitored master. In sentinel.conf it is the name after `sentinel monitor`; `SENTINEL masters` lists them.",
    "service": "The name of a service registered in Consul. `consul catalog services` lists them.",
    "bucket": "A bucket that allows anonymous reads. In MinIO: Buckets → your bucket → Access Policy → public.",
    "object": "The name of an object in that bucket, for example sample.txt.",
    "db": "The name of an existing database.",
    "promql": "A PromQL expression that returns data, for example up.",
    "realm": "The name of the realm. In the Keycloak admin console it is shown at the top left, under the Keycloak logo.",
    "tenant": "Your tenant ID or domain. Azure portal → Microsoft Entra ID → Overview → Tenant ID (or the verified domain, such as contoso.onmicrosoft.com).",
    "authServer": "The authorization server ID. In Okta: Security → API → Authorization Servers → the ID column (default is the built-in one).",
    "slug": "The application slug. In authentik: Applications → your application → the slug in its address and settings.",
    "poolId": "The user pool ID. In AWS: Cognito → User pools → your pool → User pool ID, for example us-east-1_AbCdEf.",
    "hostedUi": "The hosted UI domain. Cognito → your pool → App integration → Domain.",
    "clientId": "The ID of a client registered for this test. Create a dedicated test client in your identity provider (Clients / Applications) and copy its client ID. Do not reuse a production client.",
    "redirectUri": "A redirect URI that is registered on that client. Copy one from the client's settings: an unregistered one is refused.",
    "scope": "The scopes to request, URL-encoded with %20 between them, for example openid%20profile%20email.",
    "domain": "Your mail domain, the part after @ in your addresses, for example example.com.",
    "email": "A mailbox on that domain, for example user@example.com.",
    "mgmt": "The address of the management interface. Keycloak 25+: port 9000 (https://host:9000). RabbitMQ: http://host:15672." + HOSTDOCKER,
    "monitor": "The NATS monitoring address, usually http://host:8222. It is enabled with `-m 8222`.",
    "mtaSts": "The full address of your MTA-STS policy: https://mta-sts.<your domain>/.well-known/mta-sts.txt",
    "method": "The full gRPC method, for example /helloworld.Greeter/SayHello. `grpcurl -plaintext host:port list` lists services if reflection is enabled.",
    "health": "The health or readiness path of the service, starting with /, for example /health or /healthz.",
    "limited": "A path that is rate limited, starting with /, for example /api/v1/items.",
    "limit": "The documented rate limit in requests per second. It is only used in the job's description.",
    "origin": "The Origin your browser app sends, for example https://app.example.com. Open the app, DevTools → Network, and read the Origin header on an API call.",
    "loginPath": "The path of the login endpoint or handler, starting with /.",
    "logoutPath": "The path of the logout handler, starting with /. Shibboleth SP: /Shibboleth.sso/Logout.",
    "sessionPath": "The path of the session status handler. Shibboleth SP: /Shibboleth.sso/Session.",
    "acsPath": "The path of the assertion consumer service. Shibboleth SP: /Shibboleth.sso/SAML2/POST.",
    "metadataPath": "The path of the service provider metadata. Shibboleth SP: /Shibboleth.sso/Metadata.",
    "protectedPath": "A path that the SP protects, starting with /, for example /secure/. Open it without a session: it should redirect to the IdP.",
    "publicPath": "A public path on the same site that is not protected, usually /.",
    "uploadPath": "The path of a file upload endpoint, starting with /, from the API documentation.",
    "adfsUrl": "The AD FS address, for example https://adfs.example.com. The federation service name in AD FS Management.",
    "statusUrl": "The IdP status page. Shibboleth IdP: https://idp.example.com/idp/status (it is restricted to allowed IPs).",
    "hub": "The SignalR hub path, for example /chat. It is in your ASP.NET `MapHub<>(...)` call.",
    "ssoUrl": "The IdP's single sign-on address. Open the IdP's metadata XML and find <SingleSignOnService> with the HTTP-Redirect binding: its Location is this value.",
    "sloUrl": "The IdP's single logout address. In the metadata XML, <SingleLogoutService> with the HTTP-Redirect binding: its Location.",
    "metadataUrl": "Where the IdP publishes its SAML metadata. Keycloak: /realms/<realm>/protocol/saml/descriptor. Shibboleth: /idp/shibboleth. AD FS: /FederationMetadata/2007-06/FederationMetadata.xml.",
    "spEntityId": "The entity ID of a service provider that is REGISTERED at the IdP. In the IdP's SAML client or relying-party list, copy its entity ID (or client ID). An unregistered one is refused.",
    "acsUrl": "That service provider's assertion consumer service address, from its registration at the IdP (it must match exactly).",
    "nameId": "Any user name or ID. It is only used in logout requests.",
    "idpInitiatedUrl": "The IdP-initiated login address for a registered application. Keycloak: /realms/<realm>/protocol/saml/clients/<client URL name>. Shibboleth: /idp/profile/SAML2/Unsolicited/SSO?providerId=<entity ID>.",
    "namespace": "The target namespace of the service. Open the WSDL and read `targetNamespace` on the first line.",
    "operation": "The name of the operation to call. In the WSDL, the names under <portType><operation name=...>.",
    "param": "The name of one input element of that operation. In the WSDL, find the operation's request message and read the child element's name.",
    "value": "A value for that parameter, for example an ID that exists.",
    "soapAction": "The SOAPAction of the operation. In the WSDL, <soap:operation soapAction=\"...\"/> under the operation.",
    "etag": "The current ETag of the page. Run `curl -sI https://yoursite/page` and copy the ETag header, including the quotes.",
    "asset": "The path of a static file such as a JS, CSS or image file, for example /static/app.js. DevTools → Network lists them.",
    "bigfile": "The path of a large file, ideally 5-50 MB. The test downloads it repeatedly.",
    "user": "The user name to use with WebDAV.",
    "amqpPort": "The AMQP port, normally 5672.", "imapPort": "The IMAP port, normally 143.", "imapsPort": "The IMAPS port, normally 993.",
    "pop3Port": "The POP3 port, normally 110.", "pop3sPort": "The POP3S port, normally 995.", "smtpPort": "The SMTP port, normally 25.",
    "smtpsPort": "The SMTPS port, normally 465.", "submissionPort": "The mail submission port, normally 587.", "sshPort": "The SSH port, normally 22.",
    "ldapsPort": "The LDAPS port, normally 636.", "redisPort": "The Redis port, normally 6379.", "memcachedPort": "The Memcached port, normally 11211.",
}

# ---- credentials, by the environment variable that carries them -----------------
B64 = "Encode it like this (macOS or Linux): `printf 'USER:PASSWORD' | base64` and paste the result."
ENV_INFO = {
    "WP_ADMIN_COOKIE": {"label": "WordPress admin cookie", "guide":
        "1. Log in to the wp-admin of your STAGING site in a browser, ideally with a throwaway admin account.\n"
        "2. Open developer tools (F12, or Cmd+Option+I on a Mac), then Application (Chrome, Edge) or Storage (Firefox), then Cookies, then your site.\n"
        "3. Copy the cookie whose name starts with `wordpress_logged_in_` as name=value. For wp-admin pages also copy `wordpress_sec_...`. Join them with `; `, for example `wordpress_logged_in_abc=VALUE; wordpress_sec_abc=VALUE`.\n"
        "The cookie stops working when you log out and after a few days, so get a fresh one before each run. Anyone holding it can act as that user: keep it private."},
    "JOOMLA_ADMIN_COOKIE": {"label": "Joomla administrator cookie", "guide":
        "1. Log in to /administrator on your STAGING site.\n2. Open developer tools (F12), then Application or Storage, then Cookies.\n"
        "3. Copy the session cookie: it has a long hexadecimal name (32 characters) and a long value. Paste it as name=value.\nIt expires when you log out or after the session timeout, so get a fresh one before each run."},
    "DRUPAL_ADMIN_COOKIE": {"label": "Drupal admin session cookie", "guide":
        "1. Log in to your STAGING Drupal site as an administrator.\n2. Open developer tools (F12), then Application or Storage, then Cookies.\n"
        "3. Copy the cookie named `SESS<hash>` (or `SSESS<hash>` over HTTPS) as name=value.\nIt expires when you log out, so get a fresh one before each run."},
    "API_TOKEN": {"label": "API bearer token", "guide":
        "Usually Settings → API tokens (or Personal access tokens) in the service's admin: create a token for testing and copy it.\n"
        "For an OAuth or OIDC service, request one: `curl -s -d grant_type=client_credentials -d client_id=ID -d client_secret=SECRET https://IDP/token` and copy `access_token`.\n"
        "Tokens expire (often after 5 to 60 minutes), so create a long-lived one for long runs."},
    "API_KEY": {"label": "API key", "guide":
        "In the service's admin look for Settings → API keys (or Developer, or Integrations), create a key for testing and copy it. Give it read-only rights if the job only reads."},
    "OIDC_ACCESS_TOKEN": {"label": "Access token", "guide":
        "Request one with the client-credentials grant:\n`curl -s -d grant_type=client_credentials -d client_id=CLIENT -d client_secret=SECRET -d scope=openid https://IDP/token`\nthen copy `access_token` from the answer. Add scope=openid so userinfo calls are allowed.\nIt expires quickly (often 5 minutes): request a fresh one right before the run, or lengthen the token lifespan on the test client."},
    "OIDC_REFRESH_TOKEN": {"label": "Refresh token", "guide":
        "Sign in once as a test user and copy `refresh_token` from the token response (authorization code flow, or the password grant with scope `openid offline_access`).\nUse a client that does NOT rotate refresh tokens: with rotation only the first request works and the rest fail with invalid_grant."},
    "OIDC_CLIENT_SECRET": {"label": "Client secret", "guide":
        "In your identity provider open the test client and go to its Credentials (or Secrets) tab, then copy the client secret. Use a dedicated test client, never a production one."},
    "OIDC_CLIENT_BASIC": {"label": "Client credentials, base64", "guide":
        "The client ID and secret joined by a colon and base64-encoded, used for HTTP Basic client authentication.\n`printf 'CLIENT_ID:CLIENT_SECRET' | base64` (macOS or Linux), then paste the result."},
    "OIDC_USERNAME": {"label": "Test user name", "guide": "The user name of a test account in your identity provider. Create a dedicated test user."},
    "OIDC_PASSWORD": {"label": "Test user password", "guide": "The password of that test account. Use a dedicated test user, not a real person's account."},
    "KC_ADMIN_TOKEN": {"label": "Keycloak admin token", "guide":
        "`curl -s -d client_id=admin-cli -d grant_type=password -d username=ADMIN -d password=PASSWORD https://KEYCLOAK/realms/master/protocol/openid-connect/token` and copy `access_token`.\nIt lasts about a minute by default: raise the access token lifespan of the master realm for a long test."},
    "VAULT_TOKEN": {"label": "Vault token", "guide":
        "`vault token create -policy=default -ttl=2h` and copy `token`, or Access → Tokens in the Vault UI. Use a token with read access to the path the job reads."},
    "NC_APP_PASSWORD": {"label": "Nextcloud app password (base64)", "guide":
        "1. In Nextcloud open Settings → Security → Devices & sessions and create a new app password.\n2. " + B64 + "\nThe value is base64(user:app-password)."},
    "MEILI_KEY": {"label": "Meilisearch search key", "guide":
        "`curl -H 'Authorization: Bearer MASTER_KEY' http://HOST:7700/keys` lists the keys: copy the default Search API Key."},
    "RABBITMQ_BASIC": {"label": "RabbitMQ login (base64)", "guide": "A management user with the monitoring tag. " + B64},
    "COUCH_BASIC": {"label": "CouchDB login (base64)", "guide": "A CouchDB user that can read the database. " + B64},
    "REDIS_PASSWORD": {"label": "Redis password", "guide": "The `requirepass` value in redis.conf, or the password of the ACL user. Skip the AUTH job if your instance has no password."},
    "SOAP_USER": {"label": "SOAP service user", "guide": "A service account for WS-Security UsernameToken authentication, from the service's administrator."},
    "SOAP_PASSWORD": {"label": "SOAP service password", "guide": "The password of that service account."},
}


def annotate_guides():
    missing_vars, missing_env = [], set()
    for p in PRESETS:
        text = json.dumps(p)
        envs = sorted(set(_re.findall(r"\$\{([A-Z][A-Z0-9_]*)\}", text)))
        secrets = {}
        for e in envs:
            if e not in ENV_INFO:
                missing_env.add(e)
                continue
            secrets[e] = ENV_INFO[e]
        if secrets:
            p["secrets"] = secrets
        for v in p.get("variables", []):
            if v.get("derived") or v.get("sensitive"):
                continue
            g = VAR_GUIDES.get(v["name"])
            if g:
                v["guide"] = g
            else:
                missing_vars.append("%s.%s" % (p["id"], v["name"]))
    assert not missing_env, "credentials without a guide: %s" % sorted(missing_env)
    assert not missing_vars, "variables without a guide: %s" % missing_vars
    print("guides attached to %d presets" % len(PRESETS))


annotate_guides()

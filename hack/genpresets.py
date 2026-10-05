#!/usr/bin/env python3
"""Generates the embedded preset JSON files. Kept as a script so the library is
consistent and easy to extend; the output is what ships in the binary."""

import copy
import json
import os

OUT = os.path.join(os.path.dirname(__file__), "..", "internal", "presets", "data")
os.makedirs(OUT, exist_ok=True)

S30 = 30_000_000_000
S60 = 60_000_000_000
T15 = 15_000_000_000
T30 = 30_000_000_000


def var(name, default, description, placeholder=False, sensitive=False, derived=None):
    v = {"name": name, "default": default, "description": description}
    if derived:
        v["derived"] = derived
    if placeholder:
        v["placeholder"] = True
    if sensitive:
        v["sensitive"] = True
    return v


def job(jid, name, notes, safety, target, **kw):
    j = {
        "id": jid,
        "name": name,
        "notes": notes,
        "safety": safety,
        "job": {
            "name": name,
            "executor": kw.get("executor", "http"),
            "target": copy.deepcopy(target),
            "method": kw.get("method", "GET"),
            "concurrency": kw.get("concurrency", 20),
            "rps": kw.get("rps", 25),
            "duration": kw.get("duration", S60),
            "timeout": kw.get("timeout", T15),
            "queueSize": kw.get("queueSize", 1000),
            "onQueueFull": "reject",
            "maxWorkers": kw.get("maxWorkers", 200),
            "blockPrivate": kw.get("blockPrivate", False),
        },
    }
    if kw.get("expect"):
        j["job"]["expectStatus"] = kw["expect"]
    if kw.get("headers"):
        j["job"]["headers"] = kw["headers"]
    if kw.get("ramp"):
        j["job"]["ramp"] = kw["ramp"]
    if kw.get("db"):
        j["job"]["db"] = kw["db"]
    if kw.get("meta"):
        j["job"]["target"]["meta"] = kw["meta"]
    if kw.get("body") is not None:
        j["job"]["body"] = kw["body"]
    return j


PRESETS = []


def add(pid, title, category, summary, stack, variables, jobs):
    PRESETS.append({
        "id": pid, "title": title, "category": category,
        "summary": summary, "stack": stack,
        "variables": variables, "jobs": jobs,
    })


URL = var("url", "https://example.com", "Site base URL, no trailing slash", placeholder=True)

# ---------------------------------------------------------------- WordPress
add("wordpress", "WordPress", "CMS", "Core front end, REST API, search and wp-admin.",
    "PHP + MySQL/MariaDB + nginx/Apache",
    [URL, var("post", "hello-world", "Slug of an existing post, from its permalink", placeholder=True),
     var("adminCookie", "${WP_ADMIN_COOKIE}", "Cookie from a logged-in browser. Read from the WP_ADMIN_COOKIE env var; export it before running.", sensitive=True)],
    [
        job("front", "wordpress front page",
            "Cheapest request on the site. If this is fast but 'single post' is slow, a page cache is serving the front end and WordPress itself is untested.",
            "read", {"url": "{{url}}/"},
            concurrency=10, rps=20, headers={"Accept": "text/html"}),
        job("single-post", "wordpress single post (uncached)",
            "The honest app-tier test. Forces PHP plus a database query per request by sending no-cache.",
            "read", {"url": "{{url}}/{{post}}/"},
            headers={"Accept": "text/html", "Cache-Control": "no-cache", "Pragma": "no-cache"}),
        job("rest-posts", "wordpress REST API posts",
            "Serialises many posts through PHP, so it is markedly heavier than the front page. Good proxy for headless or API-driven use.",
            "read", {"url": "{{url}}/wp-json/wp/v2/posts?per_page=10"},
            headers={"Accept": "application/json"}),
        job("rest-single", "wordpress REST API single post",
            "One REST object: the API path most theme front ends actually use.",
            "read", {"url": "{{url}}/wp-json/wp/v2/posts/{{post}}"},
            headers={"Accept": "application/json"}),
        job("search", "wordpress search",
            "Usually the heaviest read on a WordPress site and the first thing to time out. Keep the rate low.",
            "read", {"url": "{{url}}/?s=wordpress"},
            headers={"Accept": "text/html"}, concurrency=10, rps=5, timeout=T30),
        job("feed", "wordpress RSS feed",
            "Cheap and cacheable, but it exercises the same template engine as the front page.",
            "read", {"url": "{{url}}/feed/"}, headers={"Accept": "application/rss+xml"}),
        job("admin", "wordpress wp-admin dashboard",
            "Admin screens run far more PHP per request and each one opens a database connection. Needs {{adminCookie}}. Still writes (transients, session meta), so staging only.",
            "mutating", {"url": "{{url}}/wp-admin/"},
            headers={"Accept": "text/html", "Cookie": "{{adminCookie}}"},
            concurrency=5, rps=5, timeout=T30),
        job("capacity-ramp", "wordpress capacity ramp",
            "Linear ramp to 200 rps over two minutes, then holds. Watch p99, not the average: that is where the knee is. Raise 200 to your expected peak.",
            "read", {"url": "{{url}}/{{post}}/"},
            headers={"Cache-Control": "no-cache"},
            concurrency=100, rps=200, ramp=120_000_000_000, duration=180_000_000_000,
            queueSize=5000, maxWorkers=400),
        job("soak", "wordpress 10 minute soak",
            "Steady load long enough to surface PHP-FPM pool exhaustion, MySQL connection leaks and cache decay that a short run never reaches.",
            "read", {"url": "{{url}}/{{post}}/"},
            headers={"Cache-Control": "no-cache"},
            concurrency=40, rps=30, duration=600_000_000_000, queueSize=5000),
    ])

# ---------------------------------------------------------------- Joomla
add("joomla", "Joomla", "CMS", "Joomla 4/5 front end, REST API, search and administrator.",
    "PHP + MySQL/MariaDB",
    [URL, var("article", "1", "ID of an existing article, from its menu item URL"),
     var("adminCookie", "${JOOMLA_ADMIN_COOKIE}", "Administrator session cookie, copied from a logged-in browser (DevTools > Application > Cookies; the long hex-named cookie). Read from the JOOMLA_ADMIN_COOKIE env var; export it as name=value before running.", sensitive=True)],
    [
        job("front", "joomla front page", "Baseline anonymous read.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("article", "joomla article page",
            "com_content view: renders a component through PHP with a database query.",
            "read", {"url": "{{url}}/index.php?option=com_content&view=article&id={{article}}"},
            headers={"Accept": "text/html"}),
        job("rest-articles", "joomla REST API articles",
            "The Joomla 4+ JSON:API at /api. Heavier than the front page because it serialises many rows.",
            "read", {"url": "{{url}}/api/index.php/v1/content/articles"},
            headers={"Accept": "application/json"}),
        job("com-finder", "joomla smart search (com_finder)",
            "Indexer-backed search: one of the heaviest queries in a stock Joomla install.",
            "read", {"url": "{{url}}/index.php?option=com_finder&view=search&q=product"},
            concurrency=10, rps=5, timeout=T30),
        job("login-form", "joomla login form",
            "GET only, so no lockouts. POSTing credentials repeatedly can trigger rate limiting; that is not modelled here.",
            "read", {"url": "{{url}}/index.php?option=com_users&view=login"}),
        job("administrator", "joomla administrator",
            "The back office: far heavier than the front end. Needs {{adminCookie}}. Writes session data, so staging only.",
            "mutating", {"url": "{{url}}/administrator/"},
            headers={"Cookie": "{{adminCookie}}"},
            concurrency=5, rps=5, timeout=T30),
        job("capacity-ramp", "joomla capacity ramp",
            "Ramp to 150 rps over two minutes against the article view. Watch p99.",
            "read", {"url": "{{url}}/index.php?option=com_content&view=article&id={{article}}"},
            concurrency=80, rps=150, ramp=120_000_000_000, duration=180_000_000_000,
            queueSize=5000, maxWorkers=300),
    ])

# ---------------------------------------------------------------- Drupal
add("drupal", "Drupal", "CMS", "Drupal front end, JSON:API, search and admin.",
    "PHP + MySQL/PostgreSQL + nginx/Apache",
    [URL, var("node", "1", "Numeric node ID of published content, e.g. /node/1"),
     var("adminCookie", "${DRUPAL_ADMIN_COOKIE}", "Session cookie of a logged-in Drupal admin, copied from your browser (DevTools > Application > Cookies; the cookie named SESS... or SSESS...). Read from the DRUPAL_ADMIN_COOKIE env var; export it as name=value before running.", sensitive=True)],
    [
        job("front", "drupal front page", "Baseline anonymous read.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("node", "drupal node page",
            "Renders one entity through the render system: PHP plus several queries.",
            "read", {"url": "{{url}}/node/{{node}}"},
            headers={"Accept": "text/html", "Cache-Control": "no-cache"}),
        job("jsonapi", "drupal JSON:API collection",
            "The API most headless Drupal builds consume. Serialises entities, so it is heavy.",
            "read", {"url": "{{url}}/jsonapi/node/article"},
            headers={"Accept": "application/vnd.api+json"}),
        job("search", "drupal core search",
            "Core search hits the search index tables and degrades quickly on large sites.",
            "read", {"url": "{{url}}/search/node?keys=content"},
            concurrency=10, rps=5, timeout=T30),
        job("views", "drupal views listing",
            "pager queries on large sites are a classic bottleneck.",
            "read", {"url": "{{url}}/?page=1"}),
        job("admin", "drupal admin overview",
            "Needs {{adminCookie}}. Writes session and cache data, so staging only.",
            "mutating", {"url": "{{url}}/admin"},
            headers={"Cookie": "{{adminCookie}}"},
            concurrency=5, rps=5, timeout=T30),
    ])

# ---------------------------------------------------------------- Ghost
add("ghost", "Ghost", "CMS", "Ghost blog: public pages, tag archives and the Content API.",
    "Node.js + MySQL",
    [URL, var("tag", "technology", "Slug of an existing tag, e.g. /tag/technology/")],
    [
        job("front", "ghost front page", "Cached by default; may not reach the app tier.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("post", "ghost single post (uncached)",
            "Ghost renders on the server, so this reaches Node and MySQL.",
            "read", {"url": "{{url}}/{{tag}}/"},
            headers={"Accept": "text/html", "Cache-Control": "no-cache"}),
        job("tag", "ghost tag archive", "A paginated collection view.",
            "read", {"url": "{{url}}/tag/{{tag}}/"}),
        job("content-api", "ghost Content API",
            "The v4 JSON API. Supply ?key= in the URL if the site requires a public key.",
            "read", {"url": "{{url}}/ghost/api/v4/content/posts/?limit=10"},
            headers={"Accept": "application/json"}),
        job("rss", "ghost RSS feed", "Cheap, cacheable.",
            "read", {"url": "{{url}}/rss/"}),
    ])

# ---------------------------------------------------------------- MediaWiki
add("mediawiki", "MediaWiki", "Wiki", "MediaWiki pages, API queries and search.",
    "PHP + MySQL/SQLite",
    [URL, var("page", "Main_Page", "Title of an existing page, underscores for spaces")],
    [
        job("main", "mediawiki main page", "Baseline read.",
            "read", {"url": "{{url}}/wiki/{{page}}"}, headers={"Accept": "text/html"}),
        job("api-parse", "mediawiki action=parse",
            "The API path most tooling uses; returns rendered wikitext.",
            "read", {"url": "{{url}}/w/api.php?action=parse&page={{page}}&format=json"},
            headers={"Accept": "application/json"}),
        job("api-query", "mediawiki action=query list=allpages",
            "Iterative listing query. Heavy on wikis with many pages.",
            "read", {"url": "{{url}}/w/api.php?action=query&list=allpages&aplimit=50&format=json"},
            headers={"Accept": "application/json"}),
        job("search", "mediawiki full-text search",
            "CirrusSearch or the default index; usually the heaviest read.",
            "read", {"url": "{{url}}/w/index.php?search=content&title=Special:Search"},
            concurrency=10, rps=5, timeout=T30),
        job("api-edit", "mediawiki API parse of edit form",
            "GET only: reads the edit form without submitting. POSTing edits would modify the wiki.",
            "read", {"url": "{{url}}/w/api.php?action=query&prop=revisions&titles={{page}}&format=json"},
            headers={"Accept": "application/json"}),
    ])

# ---------------------------------------------------------------- phpBB
add("phpbb", "phpBB", "Forum", "phpBB forum index, topic reads and the unread search.",
    "PHP + MySQL",
    [URL, var("topic", "1", "Numeric topic ID from viewtopic.php?t=")],
    [
        job("index", "phpBB forum index", "Baseline read of the board listing.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("viewforum", "phpBB forum view",
            "Renders a forum page including per-topic permissions and last-post info.",
            "read", {"url": "{{url}}/viewforum.php?f=1"}),
        job("viewtopic", "phpBB topic view",
            "Loads a topic plus its posts: several queries and template rendering.",
            "read", {"url": "{{url}}/viewtopic.php?t={{topic}}"}),
        job("search", "phpBB search",
            "Unread search runs an index query that grows with the board size.",
            "read", {"url": "{{url}}/search.php?keywords=content"},
            concurrency=10, rps=5, timeout=T30),
        job("ucp", "phpBB user control panel",
            "Behind auth; without a cookie this returns the login redirect, which still measures the redirect path.",
            "read", {"url": "{{url}}/ucp.php"}),
    ])

# ---------------------------------------------------------------- Discourse
add("discourse", "Discourse", "Forum", "Discourse forum: categories, topics and the JSON API.",
    "Ruby on Rails + PostgreSQL + Redis",
    [URL, var("topic", "1", "Numeric topic ID, e.g. /t/some-topic/1")],
    [
        job("front", "discourse latest", "Baseline read.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("category", "discourse category", "A topic listing with pagination.",
            "read", {"url": "{{url}}/c/general"}),
        job("topic-json", "discourse topic JSON",
            "The JSON API the web client itself uses, so it is a faithful proxy for real browsing.",
            "read", {"url": "{{url}}/t/{{topic}}.json"},
            headers={"Accept": "application/json"}),
        job("search-json", "discourse search JSON",
            "PostgreSQL full-text search across posts; heavy on large forums.",
            "read", {"url": "{{url}}/search.json?q=content"},
            concurrency=10, rps=5, timeout=T30),
        job("latest-json", "discourse latest JSON", "Pinned and latest topic listing.",
            "read", {"url": "{{url}}/latest.json"},
            headers={"Accept": "application/json"}),
    ])

# ---------------------------------------------------------------- TYPO3
add("typo3", "TYPO3", "CMS", "TYPO3 front end and the backend login flow (read-only).",
    "PHP + MySQL",
    [URL, var("page", "1", "Numeric page uid, e.g. index.php?id=1&type=98")],
    [
        job("front", "typo3 front page", "Baseline read.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("page", "typo3 rendered page",
            "Frontend rendering with the full page tree walk.",
            "read", {"url": "{{url}}/index.php?id={{page}}&type=98"},
            headers={"Cache-Control": "no-cache"}),
        job("sitemap", "typo3 sitemap", "Cached by default; a cheap sanity check.",
            "read", {"url": "{{url}}/sitemap.xml"}),
        job("backend-login", "typo3 backend login form",
            "GET only. Repeated POSTs to the backend can trip rate limiting and lock accounts.",
            "read", {"url": "{{url}}/typo3/"}, timeout=T30),
    ])

# ---------------------------------------------------------------- E-commerce
add("magento", "Magento / OpenMage", "E-commerce", "Catalog, cart and the REST API.",
    "PHP + MySQL + Elasticsearch",
    [URL, var("category", "2", "Numeric category ID from catalog/category/view/id/")],
    [
        job("front", "magento home page", "Usually heavily cached.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("category", "magento category listing",
            "Loads the category tree, the product collection and layered navigation.",
            "read", {"url": "{{url}}/catalog/category/view/id/{{category}}"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("product", "magento product page",
            "Loads one product with its related, upsell and review collections.",
            "read", {"url": "{{url}}/catalog/product/view/id/1"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("cart", "magento cart page",
            "Session-backed. A fresh session per request needs a cookie jar, which this tool does not maintain, so this measures the empty-cart path.",
            "read", {"url": "{{url}}/checkout/cart/"}, timeout=T30),
        job("rest-products", "magento REST products",
            "The REST API most integrations use.",
            "read", {"url": "{{url}}/rest/V1/products?searchCriteria[pageSize]=10"},
            headers={"Accept": "application/json"}),
    ])

add("prestashop", "PrestaShop", "E-commerce", "Catalog, search and the webservice API.",
    "PHP + MySQL",
    [URL, var("category", "2", "Numeric category id from /category/2-slug")],
    [
        job("front", "prestashop home page", "Baseline read.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("category", "prestashop category listing",
            "Facet and pagination queries over the product table.",
            "read", {"url": "{{url}}/category/{{category}}"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("product", "prestashop product page", "One product with its combinations.",
            "read", {"url": "{{url}}/product/1-x.html"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("search", "prestashop search",
            "Search across name, reference and description: expensive on large catalogs.",
            "read", {"url": "{{url}}/index.php?controller=search&s=product&search_query=product"},
            concurrency=10, rps=5, timeout=T30),
        job("webservice", "prestashop webservice API",
            "Requires an API key in the URL as ?ws_key=. GET only.",
            "read", {"url": "{{url}}/api/products?limit=10"},
            headers={"Accept": "application/json"}),
    ])

add("shopware", "Shopware 6", "E-commerce", "Storefront, product listing and the Store API.",
    "PHP + MySQL",
    [URL, var("category", "navigation-id", "Navigation category id from the storefront URL", placeholder=True)],
    [
        job("front", "shopware home page", "Cached by default.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("listing", "shopware product listing",
            "Loads the category tree plus a paginated product listing.",
            "read", {"url": "{{url}}/navigation/{{category}}"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("product", "shopware product detail", "One product with media and properties.",
            "read", {"url": "{{url}}/detail/1"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("store-api", "shopware Store API",
            "The JSON API the storefront hydrates from.",
            "read", {"url": "{{url}}/store-api/products?limit=10"},
            headers={"Accept": "application/json"}),
    ])

# ---------------------------------------------------------------- Headless CMS
add("strapi", "Strapi / Directus (headless CMS)", "Headless CMS", "REST or GraphQL content APIs.",
    "Node.js + PostgreSQL/MySQL",
    [URL, var("collection", "articles", "API id of a collection type, e.g. articles")],
    [
        job("rest-list", "headless REST collection",
            "The list endpoint behind most headless front ends.",
            "read", {"url": "{{url}}/api/{{collection}}?pagination[pageSize]=25"},
            headers={"Accept": "application/json"}),
        job("rest-single", "headless REST single entry",
            "One entry with its relations populated.",
            "read", {"url": "{{url}}/api/{{collection}}/1"},
            headers={"Accept": "application/json"}),
        job("graphql", "headless GraphQL query",
            "Edit the body for your schema. POSTs JSON to the standard /graphql endpoint.",
            "read", {"url": "{{url}}/graphql"}, method="POST",
            body='{"query":"{ data { __typename } }"}',
            headers={"Accept": "application/json", "Content-Type": "application/json"}),
    ])

# ---------------------------------------------------------------- JS frameworks
add("nextjs", "Next.js / Nuxt / SSR", "JavaScript", "Server-rendered routes and data endpoints.",
    "Node.js",
    [URL, var("route", "about", "An application route, e.g. /about")],
    [
        job("front", "SSR home page", "Baseline read of the rendered shell.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("route", "SSR application route", "A second rendered route.",
            "read", {"url": "{{url}}/{{route}}"}, headers={"Accept": "text/html"}),
        job("data-route", "framework data endpoint",
            "_next/data on Next.js, __nuxt on Nuxt. These are the per-request payloads the browser fetches after the HTML.",
            "read", {"url": "{{url}}/_next/data/{{route}}.json"},
            headers={"Accept": "application/json"}),
        job("static-asset", "static asset", "A hashed build asset, usually served by the CDN.",
            "read", {"url": "{{url}}/_next/static/chunks/main.js"}),
    ])

add("static-site", "Static site (Hugo, Jekyll, Astro)", "Static", "Prebuilt HTML, images and build metadata.",
    "Static files + CDN",
    [URL, var("page", "about", "An output path such as about or blog/post-1")],
    [
        job("front", "static home page", "Baseline read.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("page", "static inner page", "A generated subpage.",
            "read", {"url": "{{url}}/{{page}}/"}),
        job("sitemap", "sitemap.xml", "Static; a quick sanity check on routing.",
            "read", {"url": "{{url}}/sitemap.xml"}),
        job("asset", "static asset", "A hashed CSS or JS asset, usually CDN-served.",
            "read", {"url": "{{url}}/style.css"}),
        job("notfound", "404 page", "Confirms the host serves 404s rather than a 200 soft-404.",
            "read", {"url": "{{url}}/this-page-does-not-exist-12345"}),
    ])

add("lamp", "Generic LAMP / LEMP", "Generic", "Hostile starting point for any PHP or Python stack.",
    "nginx/Apache + PHP or Python",
    [URL, var("path", "/index.php", "An application entry point")],
    [
        job("root", "root document", "Baseline read of the site root.",
            "read", {"url": "{{url}}/"}, headers={"Accept": "text/html"}),
        job("app", "application entry point", "The dynamic page behind the framework.",
            "read", {"url": "{{url}}{{path}}"},
            headers={"Cache-Control": "no-cache"}, timeout=T30),
        job("notfound", "404 handling", "Verifies the host returns a real 404.",
            "read", {"url": "{{url}}/definitely-not-here-98765"}),
        job("server-info", "server headers", "Small response that confirms reachability under load.",
            "read", {"url": "{{url}}/"}, method="HEAD"),
    ])

# ---------------------------------------------------------------- API
add("api-rest", "REST API (generic)", "API", "Collection, single resource and a write.",
    "Any",
    [URL, var("collection", "items", "Collection path segment, e.g. items"),
     var("id", "1", "ID of an existing resource")],
    [
        job("list", "REST collection", "The list endpoint, usually the heaviest read.",
            "read", {"url": "{{url}}/{{collection}}?limit=25"},
            headers={"Accept": "application/json"}),
        job("item", "REST single item", "One resource by ID.",
            "read", {"url": "{{url}}/{{collection}}/{{id}}"},
            headers={"Accept": "application/json"}),
        job("head", "REST HEAD probe", "Cheap reachability check that returns headers only.",
            "read", {"url": "{{url}}/{{collection}}"}, method="HEAD"),
        job("create", "REST POST create",
            "WRITES. Needs an API token in the headers and will create real records. Staging only.",
            "write", {"url": "{{url}}/{{collection}}"}, method="POST",
            body='{"name":"blasta-loadtest","createdAt":"2026-01-01T00:00:00Z"}',
            headers={"Accept": "application/json", "Content-Type": "application/json"},
            concurrency=5, rps=5, timeout=T30),
    ])

add("api-graphql", "GraphQL API", "API", "Queries, an aliased batch, and introspection.",
    "Any",
    [URL, var("field", "node", "A field to select, used to build the query body")],
    [
        job("query", "GraphQL simple query",
            "Edit the body for your schema. POSTs a JSON query to the standard /graphql endpoint.",
            "read", {"url": "{{url}}"}, method="POST",
            body='{"query":"{ __typename }"}',
            headers={"Accept": "application/json", "Content-Type": "application/json"}),
        job("field-query", "GraphQL field query",
            "Selects one field by name; edit the selection set for your schema.",
            "read", {"url": "{{url}}"}, method="POST",
            body='{"query":"{ {{field}} { id } }"}',
            headers={"Accept": "application/json", "Content-Type": "application/json"},
            timeout=T30),
        job("introspection", "GraphQL introspection",
            "The introspection query is the heaviest standard query a GraphQL server serves.",
            "read", {"url": "{{url}}"}, method="POST",
            body='{"query":"{ __schema { types { name fields { name } } } }"}',
            headers={"Accept": "application/json", "Content-Type": "application/json"},
            concurrency=10, rps=5, timeout=T30),
    ])

# ---------------------------------------------------------------- Databases
add("postgres-read", "PostgreSQL reads", "Database", "Read-only queries. Writes are refused unless allowed.",
    "PostgreSQL",
    [var("table", "wp_posts", "An existing table to read")],
    [
        job("select-one", "postgres single-row select",
            "Cheapest possible round trip: isolates connection and network overhead from query cost.",
            "read", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT 1 AS one"},
            concurrency=10, rps=100, timeout=T15),
        job("count", "postgres count(*)",
            "A full scan on a large table. The usual first query to fall over.",
            "read", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT COUNT(*) FROM {{table}}"},
            concurrency=10, rps=25, timeout=T30),
        job("indexed", "postgres indexed lookup",
            "An indexed predicate, to separate index performance from full scans.",
            "read", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT * FROM {{table}} WHERE id = 1"},
            concurrency=10, rps=50, timeout=T15),
        job("join", "postgres two-table join",
            "Representative application read. Tune the predicates to your schema.",
            "read", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT a.id FROM {{table}} a JOIN {{table}} b ON a.id = b.id LIMIT 100"},
            concurrency=10, rps=25, timeout=T30),
        job("pool-saturation", "postgres pool saturation",
            "Concurrency deliberately above maxOpen so you can watch connection pressure. Run alongside an app-level test to see which saturates first.",
            "read", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 8, "allowWrite": False},
            meta={"query": "SELECT 1 AS one"},
            concurrency=64, rps=200, timeout=T15, queueSize=5000, maxWorkers=200),
    ])

add("postgres-write", "PostgreSQL writes", "Database", "INSERT throughput. Creates real rows: STAGING ONLY.",
    "PostgreSQL",
    [var("table", "blasta_loadtest", "Table that must already exist")],
    [
        job("insert", "postgres INSERT",
            "WRITES rows. Requires the target table to exist. Staging only.",
            "write", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 8, "allowWrite": True},
            meta={"query": "INSERT INTO {{table}} (created_at) VALUES (NOW())"},
            concurrency=8, rps=50, timeout=T30),
        job("insert-select", "postgres INSERT ... SELECT",
            "A heavier write that reads and writes in one statement, similar to real ingestion.",
            "write", {"url": ""},
            executor="sql", db={"driver": "postgres", "dsnEnv": "PG_DSN", "maxOpen": 8, "allowWrite": True},
            meta={"query": "INSERT INTO {{table}} (created_at) SELECT NOW() FROM generate_series(1, 10)"},
            concurrency=4, rps=10, timeout=T30),
    ])

add("mysql-read", "MySQL / MariaDB reads", "Database", "Read-only queries against MySQL or MariaDB.",
    "MySQL / MariaDB",
    [var("table", "wp_posts", "An existing table to read")],
    [
        job("select-one", "mysql single-row select",
            "Cheapest round trip: isolates connection overhead from query cost.",
            "read", {"url": ""},
            executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT 1 AS one"},
            concurrency=10, rps=100, timeout=T15),
        job("count", "mysql COUNT(*)",
            "A full scan. On InnoDB this is the usual first query to exhaust the buffer pool.",
            "read", {"url": ""},
            executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT COUNT(*) FROM {{table}}"},
            concurrency=10, rps=25, timeout=T30),
        job("indexed", "mysql indexed lookup",
            "An indexed primary-key lookup for comparison against the full scan.",
            "read", {"url": ""},
            executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 16, "allowWrite": False},
            meta={"query": "SELECT * FROM {{table}} WHERE ID = 1"},
            concurrency=10, rps=50, timeout=T15),
        job("pool-saturation", "mysql pool saturation",
            "Concurrency above maxOpen to expose connection pressure. This is the classic WordPress failure mode: max_connections exhausted under PHP-FPM.",
            "read", {"url": ""},
            executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 8, "allowWrite": False},
            meta={"query": "SELECT 1 AS one"},
            concurrency=64, rps=200, timeout=T15, queueSize=5000, maxWorkers=200),
    ])

add("mysql-write", "MySQL / MariaDB writes", "Database", "INSERT throughput. Creates real rows: STAGING ONLY.",
    "MySQL / MariaDB",
    [var("table", "blasta_loadtest", "Table that must already exist")],
    [
        job("insert", "mysql INSERT",
            "WRITES rows. Requires the target table to exist. Staging only.",
            "write", {"url": ""},
            executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 8, "allowWrite": True},
            meta={"query": "INSERT INTO {{table}} (created_at) VALUES (NOW())"},
            concurrency=8, rps=50, timeout=T30),
        job("batch", "mysql multi-row INSERT",
            "A heavier batched write, similar to real ingestion workloads.",
            "write", {"url": ""},
            executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 8, "allowWrite": True},
            meta={"query": "INSERT INTO {{table}} (created_at) VALUES (NOW()), (NOW()), (NOW()), (NOW()), (NOW())"},
            concurrency=4, rps=10, timeout=T30),
    ])

# ---------------------------------------------------------------- Other protocols
add("grpc-generic", "gRPC unary", "gRPC", "Unary calls against a reflection-enabled server.",
    "gRPC",
    [var("target", "127.0.0.1:50051", "Host:port of the gRPC server"),
     var("method", "/helloworld.Greeter/SayHello", "Full method path")],
    [
        job("unary", "gRPC unary call",
            "Edit the payload for your message type. The payload is raw protobuf bytes as text, so simple messages are easiest.",
            "read", {"url": "grpc://{{target}}", "meta": {"method": "{{method}}", "target": "{{target}}"}},
            executor="grpc", concurrency=10, rps=50, timeout=T15),
        job("small", "gRPC small payload",
            "Lower rate to isolate connection overhead from per-call cost.",
            "read", {"url": "grpc://{{target}}", "meta": {"method": "{{method}}", "target": "{{target}}"}},
            executor="grpc", concurrency=10, rps=100, timeout=T15),
    ])

add("websocket-generic", "WebSocket", "WebSocket", "Handshake latency and message round-trip time.",
    "WebSocket",
    # The ws executor needs a ws:// or wss:// URL, so this preset cannot reuse the
    # shared https:// base URL variable: --url https://site would render a job that
    # can never connect.
    [var("url", "wss://example.com", "WebSocket base URL, starting with ws:// or wss://", placeholder=True),
     var("path", "/socket", "WebSocket path, e.g. /ws or /socket.io/")],
    [
        job("handshake", "websocket handshake only",
            "Empty payload: measures connect plus upgrade without waiting for a server message. This is the safe default, since not every server sends unprompted.",
            "read", {"url": "{{url}}{{path}}"},
            executor="ws", concurrency=20, rps=50, timeout=T15),
        job("echo", "websocket echo round trip",
            "Sends a payload and waits for the echo. Only use this against a server that echoes; otherwise every request will hit the timeout.",
            "read", {"url": "{{url}}{{path}}"},
            executor="ws", body="ping", concurrency=10, rps=25, timeout=T15),
    ])

add("tcp-generic", "TCP service", "TCP", "Connection and round-trip time to a TCP endpoint.",
    "Any TCP service",
    [var("target", "127.0.0.1:9096", "Host:port; accepts host:port or tcp://host:port")],
    [
        job("connect", "tcp connect and close",
            "Measures connection setup only. Works against any TCP listener without needing the protocol.",
            "read", {"url": "tcp://{{target}}"},
            executor="tcp", concurrency=10, rps=50, timeout=T15),
        job("echo", "tcp echo round trip",
            "Sends a payload and expects an echo. Point this at an echo service such as the one BLASTA ships for testing.",
            "read", {"url": "tcp://{{target}}"},
            executor="tcp", body="PING", meta={"expectPrefix": "PING"}, concurrency=10, rps=50, timeout=T15),
    ])

# =====================================================================
# Scenario library. Everything below uses only what the engine does today:
# single requests with status checks, a linear ramp, and fixed headers/body.
# =====================================================================
S10 = 10_000_000_000
M2 = 120_000_000_000
M5 = 300_000_000_000
M10 = 600_000_000_000
M30 = 1_800_000_000_000
BASE = var("url", "https://example.com", "Base URL, no trailing slash", placeholder=True)
PATH = var("path", "/", "Path to test, starting with /. Pick a representative page or endpoint.")
TOKEN = var("token", "${API_TOKEN}", "Bearer token. Read from the API_TOKEN env var by the CLI.", sensitive=True)
HTML = {"Accept": "text/html"}
JSONH = {"Accept": "application/json"}
NOCACHE = {"Cache-Control": "no-cache", "Pragma": "no-cache"}

# ---------------------------------------------------------------- Test patterns
add("test-patterns", "Test patterns (any URL)", "Test patterns",
    "The standard test shapes: smoke, average load, stress, spike, breakpoint and soak.",
    "Any HTTP target",
    [BASE, PATH],
    [
        job("smoke", "smoke test",
            "One request a second for 30 seconds. Run this first: it proves the target, path and headers are right before you apply real load.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=1, rps=1, duration=S30, headers=HTML),
        job("average-load", "average load (5 min)",
            "Your normal traffic. Set rps to what production sees at a typical hour; the p95 here is your baseline to compare other runs against.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=20, rps=50, duration=M5, headers=HTML),
        job("stress", "stress ramp to 4x",
            "Ramps to 4x average over 5 minutes. Finds where latency starts to climb and which error shows up first (timeouts, 502/503, resets).",
            "read", {"url": "{{url}}{{path}}"}, concurrency=100, rps=200, ramp=M5, duration=M5 + M2,
            queueSize=5000, maxWorkers=400, headers=HTML),
        job("spike", "spike (sudden 10x)",
            "Reaches 10x average in 10 seconds, then holds. Models a newsletter, a link on social media or a failover. Watch recovery after it ends, not only the peak.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=200, rps=500, ramp=S10, duration=M2,
            queueSize=5000, maxWorkers=500, headers=HTML),
        job("breakpoint", "breakpoint (ramp until it breaks)",
            "Slow ramp to a very high rate over 10 minutes. Stop it when errors pass a few percent: the rate at that moment is your ceiling. Staging only.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=300, rps=1500, ramp=M10, duration=M10 + M2,
            queueSize=10000, maxWorkers=600, headers=HTML),
        job("soak", "soak (30 min)",
            "Moderate steady load for half an hour. Surfaces leaks and slow decay (memory, connections, disk, cache churn) that short runs never reach.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=30, rps=30, duration=M30,
            queueSize=5000, headers=HTML),
        job("max-throughput", "max throughput (closed loop)",
            "No rate limit: every worker sends as fast as the target answers. Measures raw capacity, but is not realistic traffic, so do not use it for latency SLOs.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=50, rps=0, duration=S60, headers=HTML),
    ])

# ---------------------------------------------------------------- Caching and CDN
add("cache-cdn", "Caching and CDN", "Web performance",
    "Cache hit versus bypass, conditional requests, compression, large files and redirects.",
    "CDN, reverse proxy or browser caching",
    [BASE, PATH,
     var("asset", "/static/app.js", "Path of a static asset (JS, CSS or image)"),
     var("bigfile", "/downloads/large.zip", "Path of a large file, ideally 5-50 MB"),
     var("etag", "\"replace-me\"", "Current ETag of the page, from the ETag response header (include quotes)", placeholder=True)],
    [
        job("hit", "cache hit (normal request)",
            "What a visitor gets. Compare with 'bypass': the gap is how much your cache is saving the origin.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=20, rps=100, headers=HTML),
        job("bypass", "cache bypass (origin)",
            "Sends no-cache so most CDNs and proxies go to the origin. This is the load your origin sees on a cold cache or a purge. Keep the rate modest.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=20, rps=20, headers=dict(HTML, **NOCACHE)),
        job("conditional-304", "conditional request (expect 304)",
            "Sends If-None-Match. A 304 is cheap, a 200 means validation is not working. Anything else is counted as an error.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=20, rps=100,
            headers=dict(HTML, **{"If-None-Match": "{{etag}}"}), expect=[304]),
        job("compression", "gzip / brotli responses",
            "Asks for compressed content. Measures CPU cost of compression on the server or edge, and transfer size.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=20, rps=50,
            headers=dict(HTML, **{"Accept-Encoding": "gzip, br"})),
        job("static-asset", "static asset",
            "Should be the fastest request you have. If it is not, assets are being served by the application instead of the web server or CDN.",
            "read", {"url": "{{url}}{{asset}}"}, concurrency=40, rps=300),
        job("large-download", "large file download",
            "Few, long requests. Watch transfer and the 'timeout' errors: they reveal bandwidth limits, proxy buffering and idle timeouts.",
            "read", {"url": "{{url}}{{bigfile}}"}, concurrency=5, rps=2, timeout=T30 * 4),
        job("http-to-https", "HTTP to HTTPS redirect",
            "Plain-HTTP request that should answer with a redirect. Point url at the http:// address.",
            "read", {"url": "{{url}}{{path}}"}, concurrency=10, rps=50, expect=[301, 302, 307, 308]),
        job("not-found", "404 page",
            "Error pages are often rendered by the application and skip the cache, so bots hitting dead URLs can cost more than real pages.",
            "read", {"url": "{{url}}/this-page-does-not-exist-blasta"}, concurrency=10, rps=50, expect=[404]),
    ])

# ---------------------------------------------------------------- Health and rate limits
add("health-and-limits", "Health checks and rate limiting", "Reliability",
    "Health endpoints, and verifying that rate limits and WAF rules really trigger.",
    "Load balancer, API gateway, WAF",
    [BASE, var("health", "/health", "Health or readiness endpoint path"),
     var("limited", "/api/v1/items", "An endpoint that is rate limited"),
     var("limit", "100", "The documented limit in requests per second (informational)")],
    [
        job("health", "health endpoint",
            "Load balancers poll this constantly. It must stay fast and must not touch the database unless that is the point of it.",
            "read", {"url": "{{url}}{{health}}"}, concurrency=10, rps=100, expect=[200]),
        job("under-limit", "just under the rate limit",
            "Stays below {{limit}} req/s. Every request should succeed, so any 429 is counted as an error: it means the limit is stricter than documented.",
            "read", {"url": "{{url}}{{limited}}"}, concurrency=20, rps=60, expect=[200], headers=JSONH),
        job("over-limit", "well over the rate limit",
            "Sends about 3x the limit. 429s are expected, so only 200 and 429 count as success. Errors here (5xx, timeouts) mean the limiter lets overload reach your app.",
            "read", {"url": "{{url}}{{limited}}"}, concurrency=100, rps=300, expect=[200, 429], headers=JSONH,
            queueSize=5000),
        job("burst", "burst then idle",
            "Short, hard burst: tests token-bucket limits that allow bursts and then refuse. Compare the 429 share with the over-limit job.",
            "read", {"url": "{{url}}{{limited}}"}, concurrency=100, rps=400, duration=S10, expect=[200, 429], headers=JSONH),
    ])

# ---------------------------------------------------------------- Auth APIs
add("auth-api", "Authenticated APIs", "API",
    "Bearer tokens, API keys, login endpoints, unauthorised requests and CORS preflight.",
    "REST APIs behind authentication",
    [BASE, var("endpoint", "/api/v1/me", "An authenticated endpoint"),
     var("loginPath", "/api/v1/login", "Login endpoint (POST JSON)"),
     TOKEN,
     var("apiKey", "${API_KEY}", "API key. Read from the API_KEY env var by the CLI.", sensitive=True),
     var("origin", "https://app.example.com", "Origin your browser app sends", placeholder=True)],
    [
        job("bearer", "bearer token request",
            "The steady-state cost of an authenticated call: token validation plus the handler.",
            "read", {"url": "{{url}}{{endpoint}}"}, concurrency=20, rps=50, expect=[200],
            headers=dict(JSONH, **{"Authorization": "Bearer {{token}}"})),
        job("api-key", "API key header request",
            "Same, with an API key. Slower than a bearer token if the key is looked up in a database each time.",
            "read", {"url": "{{url}}{{endpoint}}"}, concurrency=20, rps=50, expect=[200],
            headers=dict(JSONH, **{"X-API-Key": "{{apiKey}}"})),
        job("unauthorised", "unauthenticated request (expect 401)",
            "Rejected requests should be cheaper than accepted ones. If they are not, an attacker can load you without a credential.",
            "read", {"url": "{{url}}{{endpoint}}"}, concurrency=20, rps=100, expect=[401, 403], headers=JSONH),
        job("login", "login endpoint (POST)",
            "Password hashing is deliberately expensive, so login is usually the first thing to saturate CPU. Edit the JSON body for your API. This creates sessions, so staging only.",
            "mutating", {"url": "{{url}}{{loginPath}}"}, method="POST", concurrency=5, rps=5, timeout=T30,
            body="{\"username\":\"loadtest\",\"password\":\"change-me\"}",
            headers={"Content-Type": "application/json", "Accept": "application/json"}),
        job("cors-preflight", "CORS preflight (OPTIONS)",
            "Browsers send one before most cross-origin calls, so it multiplies your API traffic. It should be answered by the edge, not the application.",
            "read", {"url": "{{url}}{{endpoint}}"}, method="OPTIONS", concurrency=20, rps=100, expect=[200, 204],
            headers={"Origin": "{{origin}}", "Access-Control-Request-Method": "GET",
                     "Access-Control-Request-Headers": "authorization"}),
    ])

# ---------------------------------------------------------------- WooCommerce
add("woocommerce", "WooCommerce", "E-commerce",
    "Shop, product, Store API, cart fragments and checkout on WordPress.",
    "WordPress + WooCommerce + MySQL",
    [URL, var("product", "sample-product", "Slug of an existing product", placeholder=True)],
    [
        job("shop", "woocommerce shop page",
            "Product archive: a heavy query with taxonomy and meta joins.",
            "read", {"url": "{{url}}/shop/"}, headers=HTML, concurrency=10, rps=15, timeout=T30),
        job("product", "woocommerce product page",
            "Single product with price, stock and related products. Uncached to show the real cost.",
            "read", {"url": "{{url}}/product/{{product}}/"}, headers=dict(HTML, **NOCACHE)),
        job("store-api", "Store API products",
            "The block-based storefront reads this. Heavier than the classic pages because it serialises price and stock objects.",
            "read", {"url": "{{url}}/wp-json/wc/store/v1/products?per_page=12"}, headers=JSONH,
            concurrency=10, rps=15, expect=[200]),
        job("cart-fragments", "cart fragments (wc-ajax)",
            "The classic bottleneck: it runs on every page view for visitors with a cart and bypasses page caching. Often the first thing to fall over on a busy shop.",
            "read", {"url": "{{url}}/?wc-ajax=get_refreshed_fragments"}, method="POST",
            headers={"Accept": "application/json"}, concurrency=20, rps=30, timeout=T30),
        job("cart", "cart page",
            "Anonymous cart. A fresh session is created per request because BLASTA sends no cookies.",
            "read", {"url": "{{url}}/cart/"}, headers=HTML, concurrency=10, rps=15, timeout=T30),
        job("checkout", "checkout page",
            "Loads payment gateways and shipping calculation. Do not submit orders here; this only renders the form.",
            "read", {"url": "{{url}}/checkout/"}, headers=HTML, concurrency=5, rps=5, timeout=T30),
        job("my-account", "my account login form",
            "Login form render, the entry point for credential-stuffing traffic.",
            "read", {"url": "{{url}}/my-account/"}, headers=HTML, concurrency=10, rps=15),
    ])

# ---------------------------------------------------------------- App frameworks
add("laravel", "Laravel", "Frameworks", "Health route, CSRF cookie, JSON API and login page.",
    "PHP-FPM + Laravel",
    [URL],
    [
        job("up", "laravel /up health route",
            "Laravel 11+ built-in health route: framework boot cost with no application code.",
            "read", {"url": "{{url}}/up"}, expect=[200], concurrency=10, rps=50),
        job("home", "laravel home page",
            "Full request through middleware, session start and a Blade view.",
            "read", {"url": "{{url}}/"}, headers=HTML),
        job("api", "laravel JSON API",
            "Replace /api/user with a public API route. API routes skip the session and CSRF middleware, so they are much lighter.",
            "read", {"url": "{{url}}/api/ping"}, headers=JSONH),
        job("csrf-cookie", "sanctum CSRF cookie",
            "SPA login handshake. Every SPA page load calls it, and it starts a session each time.",
            "read", {"url": "{{url}}/sanctum/csrf-cookie"}, expect=[204, 200], concurrency=10, rps=30),
        job("login-page", "laravel login page",
            "Session start plus CSRF token generation on every render.",
            "read", {"url": "{{url}}/login"}, headers=HTML),
    ])

add("django", "Django", "Frameworks", "Public pages, the admin login, static files and a JSON API.",
    "Gunicorn/uWSGI + Django",
    [URL],
    [
        job("home", "django home page", "Baseline view through middleware and templates.",
            "read", {"url": "{{url}}/"}, headers=HTML),
        job("admin-login", "django admin login page",
            "Renders a form with a CSRF token and sets a cookie. A good canary for the worker pool.",
            "read", {"url": "{{url}}/admin/login/"}, headers=HTML, concurrency=10, rps=15),
        job("static", "django static file",
            "If this is slow, static files are going through Python. Serve them from WhiteNoise, nginx or a CDN.",
            "read", {"url": "{{url}}/static/admin/css/base.css"}, concurrency=40, rps=200),
        job("api-list", "django REST framework list",
            "Serialisation of a queryset. N+1 queries show up here as latency that grows with page size.",
            "read", {"url": "{{url}}/api/?format=json"}, headers=JSONH, expect=[200, 401, 403]),
    ])

add("rails", "Ruby on Rails", "Frameworks", "Health check, pages, assets and JSON.",
    "Puma + Rails",
    [URL],
    [
        job("up", "rails /up health check",
            "Rails 7.1+ health check: framework boot cost only.",
            "read", {"url": "{{url}}/up"}, expect=[200], concurrency=10, rps=50),
        job("home", "rails home page", "Full request through Rack middleware and ERB.",
            "read", {"url": "{{url}}/"}, headers=HTML),
        job("json", "rails JSON endpoint", "Replace with an index action serving .json.",
            "read", {"url": "{{url}}/posts.json"}, headers=JSONH, expect=[200]),
        job("asset", "rails compiled asset",
            "Should be served by the web server. If Puma serves it, you are burning a worker per asset.",
            "read", {"url": "{{url}}/assets/application.css"}, concurrency=40, rps=200, expect=[200, 404]),
    ])

add("java-web", "Java (Spring Boot / Tomcat)", "Frameworks",
    "Actuator health and metrics, pages, static files and session creation.",
    "Spring Boot, Tomcat, Jetty",
    [URL],
    [
        job("health", "actuator health",
            "Spring Boot Actuator. If it includes database or disk checks, it costs a query per call.",
            "read", {"url": "{{url}}/actuator/health"}, expect=[200], headers=JSONH),
        job("metrics", "actuator prometheus scrape",
            "A large text body assembled from every meter. Scrapers hit this every 15 seconds, so it must not be slow.",
            "read", {"url": "{{url}}/actuator/prometheus"}, concurrency=5, rps=5, expect=[200]),
        job("home", "home page",
            "Because BLASTA sends no cookies, each request creates a new HttpSession (JSESSIONID). That makes this a memory test as well as a latency test: watch heap during a long run.",
            "read", {"url": "{{url}}/"}, headers=HTML),
        job("static", "static resource",
            "Served by the servlet container's resource handler.",
            "read", {"url": "{{url}}/css/style.css"}, concurrency=40, rps=200, expect=[200, 304]),
        job("session-flood", "session creation flood",
            "Higher rate, no cookies: every request is a new session. Finds session store limits and GC pressure. Staging only.",
            "mutating", {"url": "{{url}}/"}, headers=HTML, concurrency=50, rps=100, duration=M2),
    ])

# ---------------------------------------------------------------- Identity-adjacent apps
add("nextcloud", "Nextcloud", "Self-hosted apps", "Status, login page, WebDAV and capabilities API.",
    "PHP + MySQL/PostgreSQL + Redis",
    [URL, var("user", "admin", "Nextcloud user for WebDAV"),
     var("appPassword", "${NC_APP_PASSWORD}", "App password. Read from the NC_APP_PASSWORD env var by the CLI.", sensitive=True)],
    [
        job("status", "status.php", "Cheapest endpoint: PHP boot with no database.",
            "read", {"url": "{{url}}/status.php"}, expect=[200], headers=JSONH),
        job("login", "login page", "Renders the login form and starts a session.",
            "read", {"url": "{{url}}/login"}, headers=HTML, concurrency=10, rps=15),
        job("capabilities", "OCS capabilities",
            "Polled by every desktop and mobile client. Needs an app password.",
            "read", {"url": "{{url}}/ocs/v1.php/cloud/capabilities?format=json"}, expect=[200],
            headers={"OCS-APIRequest": "true", "Accept": "application/json",
                     "Authorization": "Basic {{appPassword}}"}, concurrency=10, rps=20),
        job("webdav-propfind", "WebDAV PROPFIND (file listing)",
            "What sync clients do constantly. Heavy on the database for large folders. Value of appPassword must be base64(user:app-password).",
            "read", {"url": "{{url}}/remote.php/dav/files/{{user}}/"}, method="PROPFIND",
            headers={"Depth": "1", "Authorization": "Basic {{appPassword}}"}, concurrency=5, rps=5, timeout=T30,
            expect=[207]),
    ])

add("moodle", "Moodle", "Self-hosted apps", "Front page, login, course page and AJAX service.",
    "PHP + PostgreSQL/MySQL + Redis/Memcached",
    [URL, var("course", "2", "ID of an existing course")],
    [
        job("front", "moodle front page", "Anonymous front page.",
            "read", {"url": "{{url}}/"}, headers=HTML),
        job("login", "moodle login page", "Login form render. Heavy exam-start spikes land here.",
            "read", {"url": "{{url}}/login/index.php"}, headers=HTML, concurrency=20, rps=30),
        job("course", "moodle course page",
            "Often redirects to login for anonymous users; set expect accordingly if the course is public.",
            "read", {"url": "{{url}}/course/view.php?id={{course}}"}, headers=HTML, expect=[200, 303, 302, 303]),
        job("ajax", "moodle AJAX service",
            "Entry point for in-page calls. An unauthenticated call returns an error object, which still exercises routing and session start.",
            "read", {"url": "{{url}}/lib/ajax/service-nologin.php"}, method="POST",
            body="[{\"index\":0,\"methodname\":\"core_get_string\",\"args\":{\"stringid\":\"home\",\"component\":\"moodle\"}}]",
            headers={"Content-Type": "application/json", "Accept": "application/json"}, expect=[200]),
    ])

# ---------------------------------------------------------------- Search engines
add("elasticsearch", "Elasticsearch / OpenSearch", "Search", "Cluster health, searches, counts and indexing.",
    "Elasticsearch 7+/OpenSearch",
    [var("url", "http://localhost:9200", "Cluster URL, no trailing slash", placeholder=True),
     var("index", "my-index", "Index or alias to query", placeholder=True),
     var("term", "test", "A search term that exists in your data")],
    [
        job("health", "cluster health", "Master-node call. Cheap, but green/yellow/red matters during load.",
            "read", {"url": "{{url}}/_cluster/health"}, expect=[200], headers=JSONH, concurrency=5, rps=10),
        job("match-all", "match_all search (size 10)",
            "Baseline search cost with no scoring work.",
            "read", {"url": "{{url}}/{{index}}/_search"}, method="POST",
            body="{\"size\":10,\"query\":{\"match_all\":{}}}",
            headers={"Content-Type": "application/json"}, expect=[200]),
        job("match", "full-text match search",
            "A typical user query: analysis, scoring and fetch.",
            "read", {"url": "{{url}}/{{index}}/_search"}, method="POST",
            body="{\"size\":10,\"query\":{\"match\":{\"_all\":\"{{term}}\"}}}",
            headers={"Content-Type": "application/json"}, expect=[200]),
        job("aggregation", "terms aggregation",
            "Aggregations are memory hungry. Raise the rate slowly and watch the circuit breakers.",
            "read", {"url": "{{url}}/{{index}}/_search?size=0"}, method="POST",
            body="{\"aggs\":{\"top\":{\"terms\":{\"field\":\"_index\",\"size\":10}}}}",
            headers={"Content-Type": "application/json"}, expect=[200], concurrency=10, rps=10, timeout=T30),
        job("count", "count API", "Cheap and index-wide.",
            "read", {"url": "{{url}}/{{index}}/_count"}, expect=[200], headers=JSONH),
        job("index-doc", "index a document",
            "WRITES a document per request. Use a throwaway index and watch refresh and merge load. Staging only.",
            "write", {"url": "{{url}}/{{index}}/_doc"}, method="POST",
            body="{\"message\":\"blasta load test\",\"@timestamp\":\"2026-01-01T00:00:00Z\"}",
            headers={"Content-Type": "application/json"}, expect=[200, 201], concurrency=10, rps=50),
    ])

add("meilisearch", "Meilisearch", "Search", "Health and typo-tolerant search.",
    "Meilisearch",
    [var("url", "http://localhost:7700", "Meilisearch URL, no trailing slash", placeholder=True),
     var("index", "movies", "Index name", placeholder=True),
     var("apiKey", "${MEILI_KEY}", "Search API key. Read from the MEILI_KEY env var by the CLI.", sensitive=True)],
    [
        job("health", "health", "Unauthenticated liveness.",
            "read", {"url": "{{url}}/health"}, expect=[200], headers=JSONH),
        job("search", "search query",
            "As-you-type search sends one request per keystroke, so real traffic is several times your user count.",
            "read", {"url": "{{url}}/indexes/{{index}}/search"}, method="POST",
            body="{\"q\":\"test\",\"limit\":10}",
            headers={"Content-Type": "application/json", "Authorization": "Bearer {{apiKey}}"},
            expect=[200], concurrency=20, rps=100),
    ])

# ---------------------------------------------------------------- Real time
add("realtime", "Real-time (Socket.IO / SignalR)", "WebSocket",
    "Socket.IO polling handshake and WebSocket upgrade, plus SignalR negotiate.",
    "Socket.IO, SignalR",
    [var("url", "https://example.com", "HTTP(S) base URL", placeholder=True),
     var("wsUrl", "wss://example.com", "WebSocket base URL, starting with wss://", placeholder=True),
     var("hub", "/chat", "SignalR hub path")],
    [
        job("socketio-polling", "Socket.IO polling handshake",
            "The HTTP long-polling open request: the first thing every Socket.IO client does, even when it later upgrades.",
            "read", {"url": "{{url}}/socket.io/?EIO=4&transport=polling"}, expect=[200]),
        job("socketio-ws", "Socket.IO WebSocket handshake",
            "Direct WebSocket upgrade. Counts new connections per second a node can accept; open-connection capacity is a separate limit (file descriptors).",
            "read", {"url": "{{wsUrl}}/socket.io/?EIO=4&transport=websocket"}, executor="ws", concurrency=20, rps=50),
        job("signalr-negotiate", "SignalR negotiate",
            "The POST every SignalR client sends first. With sticky sessions it also picks the node.",
            "read", {"url": "{{url}}{{hub}}/negotiate?negotiateVersion=1"}, method="POST", expect=[200],
            headers=JSONH),
    ])

# ---------------------------------------------------------------- Observability tools
add("observability", "Grafana and Prometheus", "Operations", "Dashboards API, health and PromQL queries.",
    "Grafana, Prometheus",
    [var("url", "http://localhost:3000", "Base URL of the service, no trailing slash", placeholder=True),
     var("promql", "up", "A PromQL expression")],
    [
        job("grafana-health", "grafana /api/health", "Liveness plus a database ping.",
            "read", {"url": "{{url}}/api/health"}, expect=[200], headers=JSONH),
        job("grafana-login", "grafana login page", "Single page app shell.",
            "read", {"url": "{{url}}/login"}, headers=HTML),
        job("prom-healthy", "prometheus /-/healthy", "Liveness.",
            "read", {"url": "{{url}}/-/healthy"}, expect=[200]),
        job("prom-instant", "prometheus instant query",
            "What an alert rule evaluation does. Cost depends on cardinality, not rate.",
            "read", {"url": "{{url}}/api/v1/query?query={{promql}}"}, expect=[200], headers=JSONH,
            concurrency=10, rps=20, timeout=T30),
        job("prom-range", "prometheus range query (1h)",
            "What a dashboard panel does. Heavy: one dashboard with 30 panels is 30 of these at once. Keep the rate low.",
            "read", {"url": "{{url}}/api/v1/query_range?query={{promql}}&start=2026-01-01T00:00:00Z&end=2026-01-01T01:00:00Z&step=15"},
            expect=[200], headers=JSONH, concurrency=5, rps=5, timeout=T30),
    ])

# ---------------------------------------------------------------- TCP services
add("tcp-services", "Common TCP services", "TCP", "Redis, Memcached, SMTP and SSH probes that wait for a real reply.",
    "Redis, Memcached, SMTP, SSH",
    [var("host", "127.0.0.1", "Host name or IP", placeholder=True),
     var("redisPort", "6379", "Redis port"), var("memcachedPort", "11211", "Memcached port"),
     var("smtpPort", "25", "SMTP port"), var("sshPort", "22", "SSH port")],
    [
        job("redis-ping", "redis PING",
            "Sends an inline PING and waits for +PONG (7 bytes). Needs no password only if Redis has none; an auth error still proves the round trip.",
            "read", {"url": "tcp://{{host}}:{{redisPort}}"}, executor="tcp", body="PING\r\n",
            meta={"expectPrefix": "+PONG"}, concurrency=20, rps=200),
        job("memcached-version", "memcached version",
            "Waits for the VERSION reply. A pure network and event-loop test with no storage involved.",
            "read", {"url": "tcp://{{host}}:{{memcachedPort}}"}, executor="tcp", body="version\r\n",
            meta={"expectPrefix": "VERSION"}, concurrency=20, rps=200),
        job("smtp-banner", "SMTP banner",
            "Reads the 220 greeting. Measures how fast the mail server accepts connections, without sending any mail. Some servers delay the banner on purpose (greet pause).",
            "read", {"url": "tcp://{{host}}:{{smtpPort}}"}, executor="tcp",
            meta={"expectPrefix": "220"}, concurrency=10, rps=20, timeout=T30),
        job("ssh-banner", "SSH banner",
            "Reads the version line. Tests connection limits (MaxStartups) without authenticating. Will trigger fail2ban if it sees many rapid connections, so run it from a whitelisted host.",
            "read", {"url": "tcp://{{host}}:{{sshPort}}"}, executor="tcp",
            meta={"expectPrefix": "SSH-"}, concurrency=5, rps=10),
    ])

# ---------------------------------------------------------------- WordPress database
add("wordpress-db", "WordPress database (MySQL)", "Database",
    "The queries WordPress runs most, straight against MySQL/MariaDB.",
    "MySQL / MariaDB",
    [var("prefix", "wp_", "Table prefix")],
    [
        job("autoload", "autoloaded options size",
            "WordPress loads every autoload=yes option on every request. If this returns more than about 1 MB, every page pays for it.",
            "read", {"url": ""}, executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 8},
            meta={"query": "SELECT SUM(LENGTH(option_value)) FROM {{prefix}}options WHERE autoload IN ('yes','on','auto')"},
            concurrency=8, rps=20, timeout=T30),
        job("recent-posts", "recent published posts",
            "The main loop query. Fast with the type_status_date index, slow without it.",
            "read", {"url": ""}, executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 8},
            meta={"query": "SELECT ID, post_title FROM {{prefix}}posts WHERE post_type='post' AND post_status='publish' ORDER BY post_date DESC LIMIT 10"},
            concurrency=8, rps=100),
        job("postmeta-join", "posts joined to postmeta",
            "Typical plugin or WooCommerce query. Postmeta has no useful index on meta_value, so this degrades quickly as the table grows.",
            "read", {"url": ""}, executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 8},
            meta={"query": "SELECT p.ID FROM {{prefix}}posts p JOIN {{prefix}}postmeta m ON m.post_id=p.ID WHERE m.meta_key='_thumbnail_id' LIMIT 20"},
            concurrency=8, rps=30, timeout=T30),
        job("like-search", "LIKE search on content",
            "What the default WordPress search does: a full scan of post_content. The worst realistic read; keep the rate very low.",
            "read", {"url": ""}, executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 4},
            meta={"query": "SELECT ID FROM {{prefix}}posts WHERE post_status='publish' AND (post_title LIKE '%test%' OR post_content LIKE '%test%') LIMIT 10"},
            concurrency=4, rps=3, timeout=T30),
        job("transients", "expired transients count",
            "Large piles of expired transients bloat the options table. A scan like this also shows how much the options table is costing.",
            "read", {"url": ""}, executor="sql", db={"driver": "mysql", "dsnEnv": "MYSQL_DSN", "maxOpen": 4},
            meta={"query": "SELECT COUNT(*) FROM {{prefix}}options WHERE option_name LIKE '\\_transient\\_timeout\\_%'"},
            concurrency=4, rps=5, timeout=T30),
    ])


for _mod in ("scen_identity.py", "scen_data.py", "scen_web.py", "scen_enterprise.py", "scen_guides.py"):
    _p = os.path.join(os.path.dirname(os.path.abspath(__file__)), _mod)
    if os.path.exists(_p):
        exec(compile(open(_p).read(), _p, "exec"), globals())


def main():
    seen = set()
    for p in PRESETS:
        assert p["id"] not in seen, p["id"]
        seen.add(p["id"])
        path = os.path.join(OUT, p["id"] + ".json")
        with open(path, "w") as fh:
            json.dump(p, fh, indent=2)
            fh.write("\n")
    print("wrote %d presets to %s" % (len(PRESETS), os.path.normpath(OUT)))


if __name__ == "__main__":
    main()
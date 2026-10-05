# ======================================================================
# Caches, message brokers and infrastructure services.
# Redis and Memcached are spoken in their plain-text protocols over the tcp
# executor, with meta.expectPrefix so an error reply is counted as a failure.
# NOTE: the CLI expands $NAME and $5 in job strings, so expectations must not put a
# digit or letter right after a "$" (a trailing "$" is safe).
# Every request opens a NEW connection, so these also measure connection churn.
# ======================================================================
CRLF = "\r\n"


def R(*cmds):
    """Redis inline commands, pipelined into one write."""
    return CRLF.join(cmds) + CRLF


def tcpjob(jid, name, notes, safety, target, body=None, expect=None, hexbody=None, hexexpect=None, **kw):
    meta = dict(kw.pop("meta", {}))
    if expect:
        meta["expectPrefix"] = expect
    if hexbody:
        meta["bodyHex"] = hexbody
    if hexexpect:
        meta["expectHex"] = hexexpect
    return job(jid, name, notes, safety, target, executor="tcp", body=body, meta=meta or None, **kw)


RT = {"url": "tcp://{{host}}:{{port}}"}
RHOST = [var("host", "redis.example.com", "Redis host name", placeholder=True), var("port", "6379", "Redis port")]
BIG = "x" * 16384
WRITE_NOTE = "Writes keys named blasta:*, so they are easy to delete (redis-cli --scan --pattern 'blasta:*' | xargs redis-cli del). Use a test instance. "

add("redis", "Redis / Valkey / KeyDB / Dragonfly", "Cache",
    "Common Redis workload patterns: data types, transactions, scripting, pub/sub, streams, big values, replication wait, connection churn and ramps.",
    "Redis, Valkey, KeyDB, Dragonfly (RESP protocol)", RHOST + [var("authPassword", "${REDIS_PASSWORD}", "Password for the AUTH jobs. Read from REDIS_PASSWORD by the CLI.", sensitive=True)],
    [
        tcpjob("ping", "PING", "Round trip with no data access: network plus event loop. A reply other than +PONG (for example -NOAUTH) counts as an error.",
               "read", RT, body=R("PING"), expect="+PONG", concurrency=20, rps=200),
        tcpjob("connect-ping-quit", "connect, PING, QUIT (connection churn)", "Models clients with no connection pool. A new connection per request is what exhausts file descriptors and TIME_WAIT sockets.",
               "read", RT, body=R("PING", "QUIT"), expect="+PONG", concurrency=50, rps=500),
        tcpjob("auth-ping", "AUTH then PING", "Authenticated connection: password check plus ping. Needs REDIS_PASSWORD (CLI only; the web UI does not expand environment variables).",
               "read", RT, body=R("AUTH ${REDIS_PASSWORD}", "PING"), expect="+OK\r\n+PONG", concurrency=20, rps=200),
        tcpjob("set", "SET (string write)", WRITE_NOTE + "The simplest write; includes AOF/replication cost.",
               "write", RT, body=R("SET blasta:str hello"), expect="+OK", concurrency=20, rps=200),
        tcpjob("get", "GET (string read)", "The typical cache read. A hit starts with $5, a miss with $-1; both mean Redis answered, so run the SET job first for a hit.",
               "read", RT, body=R("GET blasta:str"), expect="$", concurrency=20, rps=300),
        tcpjob("set-get", "SET then GET (pipelined)", WRITE_NOTE + "Two commands in one round trip, the way client libraries pipeline.",
               "write", RT, body=R("SET blasta:str hello", "GET blasta:str"), expect="+OK\r\n$", concurrency=20, rps=200),
        tcpjob("setex", "SETEX (cache entry with a TTL)", WRITE_NOTE + "Typical cache fill. Many keys expiring together create an expiry storm: raise the rate and watch latency spikes.",
               "write", RT, body=R("SETEX blasta:ttl 60 cached-value"), expect="+OK", concurrency=20, rps=200),
        tcpjob("incr", "INCR (counter)", WRITE_NOTE + "Rate limiters and counters. Contention on a single hot key shows the single-threaded ceiling.",
               "write", RT, body=R("INCR blasta:counter"), expect=":", concurrency=50, rps=500),
        tcpjob("hash", "HSET and HGET (hash)", WRITE_NOTE + "Session and object storage.",
               "write", RT, body=R("HSET blasta:hash field value", "HGET blasta:hash field"), expect=":", concurrency=20, rps=200),
        tcpjob("list-queue", "LPUSH and RPOP (work queue)", WRITE_NOTE + "Simple job queue pattern (Sidekiq, Resque, RQ).",
               "write", RT, body=R("LPUSH blasta:queue job", "RPOP blasta:queue"), expect=":", concurrency=20, rps=200),
        tcpjob("set-add", "SADD and SISMEMBER (set)", WRITE_NOTE + "Tags, unique visitors, membership checks.",
               "write", RT, body=R("SADD blasta:set member", "SISMEMBER blasta:set member"), expect=":", concurrency=20, rps=200),
        tcpjob("zset", "ZADD and ZRANGE (leaderboard)", WRITE_NOTE + "Sorted sets cost O(log n) per write; the range read is the part that grows with size.",
               "write", RT, body=R("ZADD blasta:board 1 player", "ZRANGE blasta:board 0 9"), expect=":", concurrency=20, rps=100),
        tcpjob("transaction", "MULTI / EXEC (transaction)", WRITE_NOTE + "Atomic block: replies +OK, +QUEUED, +QUEUED, then the results.",
               "write", RT, body=R("MULTI", "SET blasta:tx a", "GET blasta:tx", "EXEC"), expect="+OK\r\n+QUEUED\r\n+QUEUED\r\n*2", concurrency=20, rps=100),
        tcpjob("lua", "EVAL (Lua script)", "Server-side scripting blocks Redis while it runs, so a slow script stalls every client. This one is trivial; replace it with your real script.",
               "read", RT, body=R('EVAL "return 1" 0'), expect=":1", concurrency=20, rps=200),
        tcpjob("publish", "PUBLISH (pub/sub)", "Fan-out cost grows with subscribers; with none it is nearly free.",
               "write", RT, body=R("PUBLISH blasta:channel message"), expect=":", concurrency=20, rps=200),
        tcpjob("stream", "XADD (stream append)", WRITE_NOTE + "Event log pattern. Streams grow without bound unless trimmed: add MAXLEN in real use.",
               "write", RT, body=R("XADD blasta:stream MAXLEN ~ 10000 * field value"), expect="$", concurrency=20, rps=200),
        tcpjob("scan", "SCAN (iterate keys)", "The safe way to walk the keyspace (never use KEYS * in production: it blocks the server).",
               "read", RT, body=R("SCAN 0 COUNT 100"), expect="*2", concurrency=5, rps=20),
        tcpjob("dbsize", "DBSIZE", "O(1) key count: a cheap command useful as a latency baseline.",
               "read", RT, body=R("DBSIZE"), expect=":", concurrency=10, rps=100),
        tcpjob("info", "INFO (monitoring scrape)", "Prometheus exporters and dashboards call INFO constantly. It builds an ~8 KB report, so it is far costlier than PING.",
               "read", RT, body=R("INFO"), expect="$", concurrency=5, rps=20),
        tcpjob("big-value", "SET with a 16 KB value", WRITE_NOTE + "Large values stress the network buffers and replication. Latency grows with size, and values over 1 MB are a known cause of latency spikes.",
               "write", RT, body=R("SET blasta:big " + BIG), expect="+OK", concurrency=10, rps=50, timeout=T30),
        tcpjob("replication-wait", "SET then WAIT for a replica", WRITE_NOTE + "WAIT blocks until a replica acknowledges, so this measures replication lag directly. Needs at least one replica, else it returns :0 after the timeout.",
               "write", RT, body=R("SET blasta:repl 1", "WAIT 1 100"), expect="+OK\r\n:", concurrency=10, rps=50, timeout=T30),
        tcpjob("ramp", "capacity ramp (PING)", "Ramps to a high rate to find the rate where latency climbs. PING is the least expensive command, so this is an upper bound for your real mix.",
               "read", RT, body=R("PING"), expect="+PONG", concurrency=100, rps=5000, ramp=M5, duration=M5 + M2, queueSize=10000, maxWorkers=400),
        tcpjob("soak", "soak (10 min)", "Steady mixed SET/GET load, long enough to expose memory fragmentation and AOF rewrite pauses.",
               "write", RT, body=R("SET blasta:soak v", "GET blasta:soak"), expect="+OK", concurrency=30, rps=500, duration=M10, queueSize=5000),
    ])

add("redis-sentinel", "Redis Sentinel", "Cache", "Sentinel health, master discovery and quorum checks.",
    "Redis Sentinel (port 26379)",
    [var("host", "sentinel.example.com", "Sentinel host name", placeholder=True), var("port", "26379", "Sentinel port"),
     var("master", "mymaster", "Monitored master name")],
    [
        tcpjob("ping", "PING", "Sentinel liveness.", "read", RT, body=R("PING"), expect="+PONG", concurrency=10, rps=50),
        tcpjob("get-master", "get-master-addr-by-name", "What every client does to find the master at start-up and after a failover. A reconnect storm after failover is this job at high rate.",
               "read", RT, body=R("SENTINEL get-master-addr-by-name {{master}}"), expect="*2", concurrency=20, rps=200),
        tcpjob("masters", "SENTINEL masters", "Full state of every monitored master.", "read", RT, body=R("SENTINEL masters"), expect="*", concurrency=5, rps=20),
        tcpjob("replicas", "SENTINEL replicas", "Replica list for one master.", "read", RT, body=R("SENTINEL replicas {{master}}"), expect="*", concurrency=5, rps=20),
        tcpjob("ckquorum", "SENTINEL ckquorum", "Checks that enough Sentinels can authorise a failover. A -NOQUORUM reply is counted as an error, which is the point.",
               "read", RT, body=R("SENTINEL ckquorum {{master}}"), expect="+OK", concurrency=2, rps=5),
        tcpjob("failover-storm", "reconnect storm after failover", "Simulates thousands of clients re-asking for the master at once, as happens right after a failover.",
               "read", RT, body=R("SENTINEL get-master-addr-by-name {{master}}"), expect="*2", concurrency=200, rps=1000, ramp=S10, duration=M2, queueSize=5000, maxWorkers=500),
    ])

MT = {"url": "tcp://{{host}}:{{port}}"}
MC = lambda *c: CRLF.join(c) + CRLF
add("memcached", "Memcached", "Cache", "Every common Memcached pattern: get/set, multi-get, counters, touch, CAS, stats, large items and the meta protocol.",
    "Memcached (text protocol)",
    [var("host", "memcached.example.com", "Memcached host name", placeholder=True), var("port", "11211", "Memcached port")],
    [
        tcpjob("version", "version", "Round trip with no data access.", "read", MT, body=MC("version"), expect="VERSION", concurrency=20, rps=200),
        tcpjob("connect-churn", "connect, version, quit", "Clients with no connection pool open a connection per call. Watch the -c connection limit and file descriptors.",
               "read", MT, body=MC("version", "quit"), expect="VERSION", concurrency=50, rps=500),
        tcpjob("set", "set", "Cache fill. Keys are blasta:*. Memcached has no safe bulk delete, so they expire after 60 seconds.",
               "write", MT, body=MC("set blasta:key 0 60 5", "hello"), expect="STORED", concurrency=20, rps=200),
        tcpjob("get-hit", "set then get (hit)", "A guaranteed cache hit, the fast path.",
               "write", MT, body=MC("set blasta:key 0 60 5", "hello", "get blasta:key"), expect="STORED\r\nVALUE blasta:key 0 5\r\nhello", concurrency=20, rps=300),
        tcpjob("get-miss", "get (miss)", "A miss is where the real cost lives: your database absorbs the load. END with no VALUE means a miss, which counts as success here.",
               "read", MT, body=MC("get blasta:definitely-missing-key"), expect="END", concurrency=20, rps=300),
        tcpjob("multiget", "multi-get of 3 keys", "Batched reads, the usual way to avoid round trips.",
               "write", MT, body=MC("set blasta:a 0 60 1", "1", "set blasta:b 0 60 1", "2", "set blasta:c 0 60 1", "3", "get blasta:a blasta:b blasta:c"),
               expect="STORED\r\nSTORED\r\nSTORED\r\nVALUE blasta:a", concurrency=20, rps=200),
        tcpjob("incr", "incr (counter)", "Atomic counter after creating it. Hot-key contention shows here.",
               "write", MT, body=MC("set blasta:n 0 60 1", "0", "incr blasta:n 1"), expect="STORED\r\n1", concurrency=20, rps=200),
        tcpjob("touch", "touch (extend a TTL)", "Sliding-expiration sessions.", "write", MT,
               body=MC("set blasta:t 0 60 1", "x", "touch blasta:t 120"), expect="STORED\r\nTOUCHED", concurrency=20, rps=200),
        tcpjob("cas", "gets (CAS token)", "Optimistic locking read. The reply carries a unique CAS id.",
               "write", MT, body=MC("set blasta:cas 0 60 1", "x", "gets blasta:cas"), expect="STORED\r\nVALUE blasta:cas 0 1", concurrency=20, rps=200),
        tcpjob("delete", "set then delete", "Cache invalidation.", "write", MT, body=MC("set blasta:d 0 60 1", "x", "delete blasta:d"),
               expect="STORED\r\nDELETED", concurrency=20, rps=200),
        tcpjob("big-value", "set a 64 KB item", "Large items use big slab classes and fragment memory. The default item limit is 1 MB.",
               "write", MT, body=MC("set blasta:big 0 60 65536", "x" * 65536), expect="STORED", concurrency=10, rps=50, timeout=T30),
        tcpjob("stats", "stats (monitoring scrape)", "Exporters call this every few seconds; it takes a global lock briefly.",
               "read", MT, body=MC("stats"), expect="STAT", concurrency=5, rps=20),
        tcpjob("stats-items", "stats items", "Per-slab statistics. Heavier than stats on a busy server.",
               "read", MT, body=MC("stats items"), expect="STAT", concurrency=2, rps=5),
        tcpjob("meta", "meta commands (ms / mg)", "The 1.6+ meta protocol: set then get with value. Older servers answer ERROR, which counts as a failure.",
               "write", MT, body=MC("ms blasta:m 5 T60", "hello", "mg blasta:m v"), expect="HD\r\nVA 5", concurrency=20, rps=200),
        tcpjob("ramp", "capacity ramp (version)", "Ramps to a high rate to find the event-loop ceiling.",
               "read", MT, body=MC("version"), expect="VERSION", concurrency=100, rps=5000, ramp=M5, duration=M5 + M2, queueSize=10000, maxWorkers=400),
    ])

# ---------------------------------------------------------------- Message brokers
add("rabbitmq", "RabbitMQ", "Messaging", "AMQP connection handshake, management API and health checks.",
    "RabbitMQ",
    [var("host", "rabbit.example.com", "Broker host name", placeholder=True), var("amqpPort", "5672", "AMQP port"),
     var("mgmt", "http://rabbit.example.com:15672", "Management API URL, no trailing slash", placeholder=True),
     var("basic", "${RABBITMQ_BASIC}", "base64(user:password) for the management API. Read from RABBITMQ_BASIC by the CLI.", sensitive=True)],
    [
        tcpjob("amqp-handshake", "AMQP protocol handshake", "Sends the AMQP 0-9-1 protocol header and checks that the broker answers with a Connection.Start frame. Every connection costs an Erlang process, so this is the connection-churn test.",
               "read", {"url": "tcp://{{host}}:{{amqpPort}}"}, hexbody=b"AMQP\x00\x00\x09\x01".hex(), hexexpect="010000", concurrency=50, rps=200),
        job("health-alarms", "health check: alarms", "Fails when a memory or disk alarm is active, i.e. when producers are being blocked.",
            "read", {"url": "{{mgmt}}/api/health/checks/alarms"}, concurrency=2, rps=5, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
        job("overview", "overview", "The management dashboard's main call: global message rates and totals.",
            "read", {"url": "{{mgmt}}/api/overview"}, concurrency=2, rps=5, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
        job("queues", "queue list", "Heavy with thousands of queues; dashboards poll it every few seconds. Add ?page=1&page_size=100 in real use.",
            "read", {"url": "{{mgmt}}/api/queues?page=1&page_size=100"}, concurrency=2, rps=2, timeout=T30, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
        job("connections", "connection list", "Every client connection with statistics. Expensive at thousands of connections.",
            "read", {"url": "{{mgmt}}/api/connections?page=1&page_size=100"}, concurrency=2, rps=2, timeout=T30, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
        job("unauthenticated", "management API without credentials", "Must be a fast 401.", "read", {"url": "{{mgmt}}/api/overview"},
            concurrency=10, rps=50, expect=[401]),
    ])
def _mqtt_connect(level, props=b""):
    var_hdr = tlv(0x00, b"")[:0] + b"\x00\x04MQTT" + bytes([level, 0x02]) + b"\x00\x3c" + props
    payload = b"\x00\x04blst"
    body = var_hdr + payload
    return (bytes([0x10, len(body)]) + body).hex()


add("mqtt", "MQTT broker (Mosquitto / EMQX / HiveMQ / VerneMQ)", "Messaging", "MQTT v3.1.1 and v5 connect handshakes with the CONNACK checked, plus reconnect-storm ramps.",
    "Any MQTT broker",
    [var("host", "mqtt.example.com", "Broker host name", placeholder=True), var("port", "1883", "MQTT port (1883; 8883 is TLS and is not supported here)")],
    [
        tcpjob("connect-311", "CONNECT v3.1.1", "A real MQTT CONNECT (clean session) with the 'connection accepted' CONNACK checked: the cost of every device reconnect. Every request uses the same client id, so a broker that enforces unique ids will drop the previous connection: that is the session-takeover path, and it is expected.",
               "read", RT, hexbody=_mqtt_connect(4), hexexpect="20020000", concurrency=50, rps=200),
        tcpjob("connect-v5", "CONNECT v5", "MQTT 5 CONNECT with empty properties. Only the CONNACK packet type is checked: a v5 CONNACK carries a variable-length property list, so its reason code cannot be matched byte for byte. A broker without v5 support closes the connection or answers with another packet, counted as a failure.",
               "read", RT, hexbody=_mqtt_connect(5, b"\x00"), hexexpect="20", concurrency=50, rps=200),
        tcpjob("connect-ramp", "reconnect storm ramp", "Ramps connections per second to find the accept ceiling: the fleet-reconnect after a broker restart or network blip.",
               "read", RT, hexbody=_mqtt_connect(4), hexexpect="20020000", concurrency=200, rps=2000, ramp=M5, duration=M5 + M2, queueSize=10000, maxWorkers=500),
        tcpjob("tcp-accept", "TCP accept only", "Connection accept with no MQTT traffic: kernel and listener backlog.", "read", RT, concurrency=100, rps=500),
    ])
add("nats", "NATS", "Messaging", "NATS server greeting and monitoring endpoints.", "NATS",
    [var("host", "nats.example.com", "Server host name", placeholder=True), var("port", "4222", "Client port"),
     var("monitor", "http://nats.example.com:8222", "Monitoring URL, no trailing slash", placeholder=True)],
    [
        tcpjob("info", "connect and read INFO", "A NATS server speaks first with an INFO line, so a connect measures the handshake and checks the greeting.",
               "read", RT, expect="INFO {", concurrency=50, rps=200),
        job("healthz", "monitoring: healthz", "Liveness for orchestrators.", "read", {"url": "{{monitor}}/healthz"}, concurrency=5, rps=20, expect=[200]),
        job("varz", "monitoring: varz", "General server state, scraped by exporters.", "read", {"url": "{{monitor}}/varz"}, concurrency=3, rps=10, expect=[200]),
        job("connz", "monitoring: connz", "Lists client connections: heavy at high connection counts.", "read", {"url": "{{monitor}}/connz"}, concurrency=2, rps=2, timeout=T30, expect=[200]),
    ])

# ---------------------------------------------------------------- Infrastructure services
add("etcd", "etcd", "Infrastructure", "Health, version and key-value reads and writes through the gRPC gateway.", "etcd v3",
    [var("url", "http://etcd.example.com:2379", "etcd client URL, no trailing slash", placeholder=True)],
    [
        job("health", "health", "Fails when the member has no leader or the quorum is lost.", "read", {"url": "{{url}}/health"}, concurrency=5, rps=20, expect=[200]),
        job("version", "version", "Cheapest call.", "read", {"url": "{{url}}/version"}, concurrency=10, rps=50, expect=[200]),
        job("range", "key read (linearizable)", "Linearizable reads go through the leader. Kubernetes API servers do this constantly.",
            "read", {"url": "{{url}}/v3/kv/range"}, method="POST", body="{\"key\":\"Ymxhc3Q=\"}", headers={"Content-Type": "application/json"}, concurrency=20, rps=100, expect=[200]),
        job("range-serializable", "key read (serializable)", "Served by any member without asking the leader: faster, possibly stale.",
            "read", {"url": "{{url}}/v3/kv/range"}, method="POST", body="{\"key\":\"Ymxhc3Q=\",\"serializable\":true}", headers={"Content-Type": "application/json"}, concurrency=20, rps=200, expect=[200]),
        job("put", "key write", "Every write is a Raft round trip plus an fsync, so latency is bounded by disk speed. Writes key 'blasta'. Test cluster only.",
            "write", {"url": "{{url}}/v3/kv/put"}, method="POST", body="{\"key\":\"Ymxhc3Q=\",\"value\":\"dmFsdWU=\"}", headers={"Content-Type": "application/json"}, concurrency=10, rps=50, expect=[200]),
        job("metrics", "metrics scrape", "Large Prometheus body; includes the disk fsync and Raft proposal histograms you should be watching during the write test.",
            "read", {"url": "{{url}}/metrics"}, concurrency=2, rps=2, expect=[200]),
    ])
add("consul", "HashiCorp Consul", "Infrastructure", "Leader, catalog, service health, KV and agent calls.", "Consul",
    [var("url", "http://consul.example.com:8500", "Consul URL, no trailing slash", placeholder=True),
     var("service", "web", "A registered service name")],
    [
        job("leader", "status: leader", "Cheapest call; fails when there is no leader.", "read", {"url": "{{url}}/v1/status/leader"}, concurrency=5, rps=30, expect=[200]),
        job("services", "catalog: services", "Service discovery listing.", "read", {"url": "{{url}}/v1/catalog/services"}, concurrency=10, rps=50, expect=[200]),
        job("health-service", "health: service instances", "The call load balancers and sidecars make constantly (consul-template, Envoy).",
            "read", {"url": "{{url}}/v1/health/service/{{service}}?passing=true"}, concurrency=20, rps=100, expect=[200]),
        job("health-blocking", "health: stale read", "?stale lets any server answer, which is how you scale reads.",
            "read", {"url": "{{url}}/v1/health/service/{{service}}?passing=true&stale"}, concurrency=20, rps=200, expect=[200]),
        job("kv-get", "KV read", "A missing key is a 404, which is expected here.", "read", {"url": "{{url}}/v1/kv/blasta/key"}, concurrency=20, rps=100, expect=[200, 404]),
        job("kv-put", "KV write", "Writes go through Raft. Writes key blasta/key. Test cluster only.", "write", {"url": "{{url}}/v1/kv/blasta/key"}, method="PUT", body="value", concurrency=10, rps=50, expect=[200]),
        job("agent-self", "agent self", "Agent configuration, polled by monitoring.", "read", {"url": "{{url}}/v1/agent/self"}, concurrency=5, rps=10, expect=[200]),
    ])
add("vault", "HashiCorp Vault", "Infrastructure", "Health, seal status and token lookups.", "Vault",
    [var("url", "https://vault.example.com:8200", "Vault URL, no trailing slash", placeholder=True),
     var("token", "${VAULT_TOKEN}", "A token. Read from VAULT_TOKEN by the CLI.", sensitive=True)],
    [
        job("health", "sys/health", "Vault answers with a status code per state: 200 active, 429 standby, 472 and 473 DR/performance standby. Standby counts as healthy here.",
            "read", {"url": "{{url}}/v1/sys/health"}, concurrency=5, rps=30, expect=[200, 429, 472, 473]),
        job("seal-status", "sys/seal-status", "Unauthenticated seal state.", "read", {"url": "{{url}}/v1/sys/seal-status"}, concurrency=5, rps=30, expect=[200]),
        job("token-lookup", "auth/token/lookup-self", "Token validation, the call apps make before using a secret. Needs a token.",
            "read", {"url": "{{url}}/v1/auth/token/lookup-self"}, concurrency=10, rps=50, expect=[200], headers={"X-Vault-Token": "{{token}}"}),
        job("kv-read", "secret read (KV v2)", "Reading a secret from secret/data/blasta. Every read is audit-logged, so a slow audit device slows Vault.",
            "read", {"url": "{{url}}/v1/secret/data/blasta"}, concurrency=10, rps=50, expect=[200, 404], headers={"X-Vault-Token": "{{token}}"}),
        job("kv-write", "secret write (KV v2)", "Writes secret/data/blasta. Test namespace only.",
            "write", {"url": "{{url}}/v1/secret/data/blasta"}, method="POST", body="{\"data\":{\"k\":\"v\"}}", concurrency=5, rps=20, expect=[200, 204],
            headers={"X-Vault-Token": "{{token}}", "Content-Type": "application/json"}),
        job("invalid-token", "request with an invalid token", "Must be a fast 403.", "read", {"url": "{{url}}/v1/auth/token/lookup-self"}, concurrency=10, rps=50, expect=[403], headers={"X-Vault-Token": "blasta-invalid"}),
    ])
add("minio", "MinIO / S3-compatible storage", "Data services", "Health probes, public object reads and bucket listing.", "MinIO, Ceph RGW, S3",
    [var("url", "https://minio.example.com", "Endpoint URL, no trailing slash", placeholder=True),
     var("bucket", "public", "A bucket with anonymous read access", placeholder=True),
     var("object", "sample.txt", "An object in that bucket", placeholder=True)],
    [
        job("live", "health: live", "MinIO liveness.", "read", {"url": "{{url}}/minio/health/live"}, concurrency=5, rps=30, expect=[200]),
        job("ready", "health: ready", "Readiness.", "read", {"url": "{{url}}/minio/health/ready"}, concurrency=5, rps=30, expect=[200]),
        job("cluster", "health: cluster", "Fails when write quorum is lost.", "read", {"url": "{{url}}/minio/health/cluster"}, concurrency=2, rps=5, expect=[200]),
        job("get-object", "GET object", "Small object reads: metadata lookup plus disk read.", "read", {"url": "{{url}}/{{bucket}}/{{object}}"}, concurrency=20, rps=200, expect=[200]),
        job("head-object", "HEAD object", "Metadata only, what clients do before downloads.", "read", {"url": "{{url}}/{{bucket}}/{{object}}"}, method="HEAD", concurrency=20, rps=200, expect=[200]),
        job("list-bucket", "list bucket (v2)", "Listing walks metadata and is far costlier than a read on big buckets.", "read", {"url": "{{url}}/{{bucket}}?list-type=2&max-keys=100"}, concurrency=5, rps=20, expect=[200]),
        job("get-missing", "GET missing object", "404 path: should be as cheap as a hit.", "read", {"url": "{{url}}/{{bucket}}/blasta-no-such-object"}, concurrency=20, rps=100, expect=[404]),
        job("range-read", "ranged GET (first 1 KB)", "What video players and download managers do.", "read", {"url": "{{url}}/{{bucket}}/{{object}}"}, concurrency=20, rps=100, expect=[200, 206], headers={"Range": "bytes=0-1023"}),
    ])
add("clickhouse", "ClickHouse", "Data services", "HTTP interface: ping, trivial and heavy queries.", "ClickHouse",
    [var("url", "http://clickhouse.example.com:8123", "HTTP interface URL, no trailing slash", placeholder=True)],
    [
        job("ping", "ping", "HTTP liveness.", "read", {"url": "{{url}}/ping"}, concurrency=5, rps=30, expect=[200]),
        job("select-one", "SELECT 1", "Parsing and network only.", "read", {"url": "{{url}}/?query=SELECT%201"}, concurrency=20, rps=200, expect=[200]),
        job("scan-numbers", "scan 10 million generated rows", "CPU-bound aggregation with no tables involved: shows how many cores one query can use. Concurrent copies compete for threads.",
            "read", {"url": "{{url}}/?query=SELECT%20sum(number)%20FROM%20numbers(10000000)"}, concurrency=5, rps=5, timeout=T30, expect=[200]),
        job("group-by", "GROUP BY on generated data", "Memory-hungry aggregation. Raise the rate slowly: ClickHouse protects itself with max_memory_usage and fails the query, which shows as an error.",
            "read", {"url": "{{url}}/?query=SELECT%20number%25100%20k,count()%20FROM%20numbers(5000000)%20GROUP%20BY%20k"}, concurrency=5, rps=3, timeout=T30, expect=[200]),
        job("system-metrics", "system.metrics", "What monitoring reads.", "read", {"url": "{{url}}/?query=SELECT%20*%20FROM%20system.metrics%20FORMAT%20JSON"}, concurrency=2, rps=5, expect=[200]),
    ])
add("influxdb", "InfluxDB", "Data services", "Health, ping and queries on InfluxDB 1.x and 2.x.", "InfluxDB",
    [var("url", "http://influx.example.com:8086", "InfluxDB URL, no trailing slash", placeholder=True),
     var("db", "mydb", "Database name (v1)", placeholder=True)],
    [
        job("ping", "ping", "Cheapest liveness call.", "read", {"url": "{{url}}/ping"}, concurrency=5, rps=30, expect=[204]),
        job("health", "health", "Health with component status (v2 and 1.8+).", "read", {"url": "{{url}}/health"}, concurrency=5, rps=30, expect=[200]),
        job("show-databases", "SHOW DATABASES (v1)", "Metadata query.", "read", {"url": "{{url}}/query?q=SHOW%20DATABASES"}, concurrency=5, rps=20, expect=[200]),
        job("select-recent", "recent points (v1)", "A dashboard-style query. Replace the measurement.",
            "read", {"url": "{{url}}/query?db={{db}}&q=SELECT%20*%20FROM%20cpu%20WHERE%20time%20%3E%20now()%20-%205m%20LIMIT%20100"}, concurrency=10, rps=30, timeout=T30, expect=[200]),
        job("write-point", "write one point (v1)", "Line-protocol write: the ingestion path. Writes measurement blasta_test. Test database only.",
            "write", {"url": "{{url}}/write?db={{db}}"}, method="POST", body="blasta_test,host=loadtest value=1", concurrency=10, rps=200, expect=[204]),
        job("write-batch", "write a batch of 20 points", "Batched ingestion, the efficient way.", "write", {"url": "{{url}}/write?db={{db}}"}, method="POST",
            body="\n".join("blasta_test,host=loadtest,n=%d value=%d" % (i, i) for i in range(20)), concurrency=10, rps=100, expect=[204]),
    ])
add("couchdb", "CouchDB", "Data services", "Liveness, database info, document reads and writes.", "Apache CouchDB",
    [var("url", "http://couch.example.com:5984", "CouchDB URL, no trailing slash", placeholder=True),
     var("db", "mydb", "Database name", placeholder=True),
     var("basic", "${COUCH_BASIC}", "base64(user:password). Read from COUCH_BASIC by the CLI.", sensitive=True)],
    [
        job("up", "_up", "Liveness; the check a load balancer uses.", "read", {"url": "{{url}}/_up"}, concurrency=5, rps=30, expect=[200]),
        job("root", "welcome document", "Version information.", "read", {"url": "{{url}}/"}, concurrency=5, rps=30, expect=[200]),
        job("db-info", "database info", "Document count and sizes.", "read", {"url": "{{url}}/{{db}}"}, concurrency=10, rps=50, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
        job("all-docs", "_all_docs (first 10)", "The primary index scan. Pagination with skip gets slow on large databases; use start_key in real code.",
            "read", {"url": "{{url}}/{{db}}/_all_docs?limit=10"}, concurrency=10, rps=50, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
        job("create-doc", "create a document", "Writes one document per request. Test database only.", "write", {"url": "{{url}}/{{db}}"}, method="POST",
            body="{\"type\":\"blasta\",\"note\":\"load test\"}", concurrency=10, rps=50, expect=[201, 202],
            headers={"Authorization": "Basic {{basic}}", "Content-Type": "application/json"}),
        job("all-dbs", "_all_dbs", "Lists every database: slow with many.", "read", {"url": "{{url}}/_all_dbs"}, concurrency=2, rps=5, expect=[200], headers={"Authorization": "Basic {{basic}}"}),
    ])

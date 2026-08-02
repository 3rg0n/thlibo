package processors

// Parity fixtures captured from the retired Python cordon-filter
// (processors/cordon-filter/run.py) by running its _signature() and
// _level() over each line while the script still existed. This table is
// the port specification, and it cannot be regenerated - the Python is
// gone, so a change here is a behaviour change, not a test fix.
var cordonParityCases = []struct {
	name  string
	line  string
	sig   string
	level string
}{
	{
		name:  "traefik-get",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/users;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-post",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"POST\", \"RequestPath\": \"/api/users\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=post;downstreamstatus=2xx;requestpath=/api/users;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-503",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"503\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=5xx;requestpath=/api/users;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-502",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"502\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=5xx;requestpath=/api/users;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-504",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"504\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=5xx;requestpath=/api/users;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-projects",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/projects\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/projects;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-id-42",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users/42\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/users/<n>;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-id-99",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users/99\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/users/<n>;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-uuid-seg",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users/3f2504e0-4f89-11d3-9a0c-0305e82c3301/edit\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/users/<uuid>/edit;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-hex-seg",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/blobs/deadbeefcafe1234\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/blobs/<hex>;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-5-segments",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/a/b/c/d/e\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/a/b/c/d;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-query",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/api/users?page=2#frag\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/api/users;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "traefik-root",
		line:  "{\"ts\": \"1778538117644029594\", \"labels\": {\"ClientAddr\": \"10.90.96.4:55298\", \"ClientHost\": \"10.90.96.4\", \"DownstreamStatus\": \"200\", \"RequestMethod\": \"GET\", \"RequestPath\": \"/\", \"RouterName\": \"api@file\", \"ServiceName\": \"api@file\", \"level\": \"info\", \"detected_level\": \"info\", \"service_name\": \"things-traefik\"}}",
		sig:   "level=info;detected_level=info;requestmethod=get;downstreamstatus=2xx;requestpath=/;routername=api-file;servicename=api-file;service_name=things-traefik",
		level: "info",
	},
	{
		name:  "things-warn-a",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"server/directory.go:366\", \"level\": \"warn\", \"detected_level\": \"warn\", \"msg\": \"x\", \"service_name\": \"things-api\"}}",
		sig:   "level=warn;detected_level=warn;caller=server/directory-go-366;msg=x;service_name=things-api",
		level: "warn",
	},
	{
		name:  "things-warn-b",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"server/toolratelimit.go:154\", \"level\": \"warn\", \"detected_level\": \"warn\", \"msg\": \"y\", \"service_name\": \"things-api\"}}",
		sig:   "level=warn;detected_level=warn;caller=server/toolratelimit-go-154;msg=y;service_name=things-api",
		level: "warn",
	},
	{
		name:  "things-msg-a",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"jobs/queue.go:105\", \"level\": \"warn\", \"detected_level\": \"warn\", \"msg\": \"Retry exhausted for task id=de594f47\", \"service_name\": \"things-api\"}}",
		sig:   "level=warn;detected_level=warn;caller=jobs/queue-go-105;msg=retry-exhausted-for;service_name=things-api",
		level: "warn",
	},
	{
		name:  "things-msg-b",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"llm/client.go:732\", \"level\": \"warn\", \"detected_level\": \"warn\", \"msg\": \"invalid character looking for beginning of value\", \"service_name\": \"things-api\"}}",
		sig:   "level=warn;detected_level=warn;caller=llm/client-go-732;msg=invalid-character-looking;service_name=things-api",
		level: "warn",
	},
	{
		name:  "things-panic",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"panic\", \"detected_level\": \"panic\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "level=panic;detected_level=panic;caller=x-go-1;msg=msg;service_name=things-api",
		level: "error",
	},
	{
		name:  "things-ERR",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"ERR\", \"detected_level\": \"ERR\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "level=err;detected_level=err;caller=x-go-1;msg=msg;service_name=things-api",
		level: "error",
	},
	{
		name:  "things-critical",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"critical\", \"detected_level\": \"critical\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "level=critical;detected_level=critical;caller=x-go-1;msg=msg;service_name=things-api",
		level: "error",
	},
	{
		name:  "things-bogus-level",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"notalevel\", \"detected_level\": \"notalevel\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "level=notalevel;detected_level=notalevel;caller=x-go-1;msg=msg;service_name=things-api",
		level: "unknown",
	},
	{
		name:  "things-empty-level",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"\", \"detected_level\": \"\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "caller=x-go-1;msg=msg;service_name=things-api",
		level: "unknown",
	},
	{
		name:  "things-fatal",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"fatal\", \"detected_level\": \"fatal\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "level=fatal;detected_level=fatal;caller=x-go-1;msg=msg;service_name=things-api",
		level: "fatal",
	},
	{
		name:  "things-trace",
		line:  "{\"ts\": \"1778627939103000000\", \"labels\": {\"caller\": \"x.go:1\", \"level\": \"trace\", \"detected_level\": \"trace\", \"msg\": \"msg\", \"service_name\": \"things-api\"}}",
		sig:   "level=trace;detected_level=trace;caller=x-go-1;msg=msg;service_name=things-api",
		level: "trace",
	},
	{
		name:  "plain-saml",
		line:  "ERROR  saml.callback: signature validation failed for assertion id=_a3f1c5; clock skew 412s exceeds tolerance",
		sig:   "error-saml-callback-signature-validation-failed-for-assertion-id-a<n>f<n>c<n>-cl",
		level: "error",
	},
	{
		name:  "plain-atlas",
		line:  "INFO  knowledge-atlas: long-poll subscriber held open for 894s on /v1/atlas/stream (sse, 124 events flushed)",
		sig:   "info-knowledge-atlas-long-poll-subscriber-held-open-for-<n>s-on-v<n>-atlas-strea",
		level: "info",
	},
	{
		name:  "plain-empty",
		line:  "",
		sig:   "unknown",
		level: "unknown",
	},
	{
		name:  "plain-spaces",
		line:  "   ",
		sig:   "unknown",
		level: "unknown",
	},
	{
		name:  "plain-broken-json",
		line:  "{not really json at all 200 GET /api/foo",
		sig:   "not-really-json-at-all-<n>-get-api-foo",
		level: "unknown",
	},
	{
		name:  "plain-access-log",
		line:  "10.90.96.4 - - [ts=1747843200] \"GET /api/v1/users\" 200 1432",
		sig:   "<ip>-ts-<hex>-get-api-v<n>-users-<n>-<n>",
		level: "unknown",
	},
	{
		name:  "plain-uuid-ip-hex",
		line:  "req 3f2504e0-4f89-11d3-9a0c-0305e82c3301 from 192.168.0.7 sha deadbeefcafe1234 took 42ms",
		sig:   "req-<uuid>-from-<ip>-sha-<hex>-took-<n>ms",
		level: "unknown",
	},
	{
		name:  "plain-oom",
		line:  "FATAL  jvm: java.lang.OutOfMemoryError: Java heap space at com.example.cache.LRUCache.put(LRUCache.java:117)",
		sig:   "fatal-jvm-java-lang-outofmemoryerror-java-heap-space-at-com-example-cache-lrucac",
		level: "fatal",
	},
	{
		name:  "json-prefix",
		line:  "ts=\"2026-01-01\" {\"level\":\"error\",\"msg\":\"disk full on /var/lib\",\"status\":507}",
		sig:   "level=error;status=5xx;msg=disk-full-on",
		level: "error",
	},
	{
		name:  "json-numeric-level",
		line:  "{\"level\": 3, \"msg\": \"numeric level\"}",
		sig:   "level=3;msg=numeric-level",
		level: "unknown",
	},
	{
		name:  "json-bool-field",
		line:  "{\"level\": \"info\", \"msg\": true}",
		sig:   "level=info;msg=true",
		level: "info",
	},
	{
		name:  "json-nested-attrs",
		line:  "{\"attributes\": {\"level\": \"error\", \"http.method\": \"PUT\", \"url.path\": \"/v2/things/7\"}}",
		sig:   "level=error;method=put;path=/v2/things/<n>",
		level: "error",
	},
	{
		name:  "json-fields-nest",
		line:  "{\"fields\": {\"level\": \"debug\", \"event\": \"cache-miss\"}}",
		sig:   "level=debug;event=cache-miss",
		level: "debug",
	},
	{
		name:  "json-array-value",
		line:  "{\"level\": \"warn\", \"msg\": [\"a\", \"b\"]}",
		sig:   "level=warn",
		level: "warn",
	},
	{
		name:  "json-not-object",
		line:  "[1, 2, 3]",
		sig:   "<n>-<n>-<n>",
		level: "unknown",
	},
	{
		name:  "json-no-known-keys",
		line:  "{\"alpha\": \"beta\", \"gamma\": 42}",
		sig:   "alpha-beta-gamma-<n>",
		level: "unknown",
	},
	{
		name:  "json-status-2digit",
		line:  "{\"level\": \"info\", \"status\": \"42\"}",
		sig:   "level=info;status=42",
		level: "info",
	},
	{
		name:  "json-msg-no-alpha",
		line:  "{\"level\": \"info\", \"msg\": \"12345 -- 678\"}",
		sig:   "level=info",
		level: "info",
	},
	{
		name:  "json-long-sig",
		line:  "{\"level\": \"info\", \"msg\": \"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\", \"RequestPath\": \"/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/seg/\"}",
		sig:   "level=info;requestpath=/seg/seg/seg/seg;msg=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		level: "info",
	},
}

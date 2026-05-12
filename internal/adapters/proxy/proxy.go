// Package proxy implements thlibo's proxy mode: a loopback HTTP server on
// 127.0.0.1:47321 that intercepts Read/Glob/Grep tool calls (running their
// output through the middleware pipeline) and forwards all other Anthropic
// API traffic verbatim. Enabled by setting ANTHROPIC_BASE_URL.
package proxy

// Package mcp implements a Model Context Protocol (MCP) server that
// exposes sshx's tool layer (internal/tool.Registry) to external
// clients such as Claude Desktop, Cursor, or any MCP-compatible agent.
//
// Transport: HTTP request/response JSON-RPC 2.0, mounted at /api/mcp on
// the running sshx process. The process owns the live SSH sessions, so
// every tool call resolves a nodeId to an in-memory session.
//
// Routing: sshx is multi-host, but MCP tool schemas describe one host.
// We therefore accept a vendor "nodeId" field inside tools/call
// arguments (stripped before dispatch) to target the host. A client can
// also bind a default node at initialize via params._sshx.nodeId.
package mcp

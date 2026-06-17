---
title: MCP Server
description: The tools CodeGraph exposes to AI agents over MCP.
---

CodeGraph runs as a [Model Context Protocol](https://modelcontextprotocol.io/) server.

## Serving modes

For a local agent connected to the current checkout, start the stdio MCP server with:

```bash
codegraph serve --mcp
```

Agents configured by the installer launch this automatically. When a `.codegraph/` index exists, the agent uses the tools below.

For a single repository exposed over HTTP, run:

```bash
codegraph serve --mcp --http --host 0.0.0.0 --port 3000 --path /workspace/repo
```

For a cloud-native multi-repo gateway, run the HTTP MCP server with a gateway repository list:

```bash
codegraph serve --mcp --http --host 0.0.0.0 --port 3000 --gateway-repos /etc/codegraph-gateway/repos.json
```

The Kubernetes operator manages that gateway mode for you. Agents connect to one URL, usually `https://<host>/mcp`, and gateway tools are prefixed by repository, such as `api__codegraph_explore`.

## Tools

| Tool | Purpose |
|---|---|
| `codegraph_search` | Find symbols by name across the codebase |
| `codegraph_callers` | Find what calls a function |
| `codegraph_callees` | Find what a function calls |
| `codegraph_impact` | Analyze what code is affected by changing a symbol |
| `codegraph_node` | Get details about a specific symbol (optionally with source code) |
| `codegraph_explore` | Return source for several related symbols grouped by file, plus a relationship map, in one call |
| `codegraph_files` | Get the indexed file structure (faster than filesystem scanning) |
| `codegraph_status` | Check index health and statistics |

## How agents should use it

CodeGraph *is* the pre-built search index. For "how does X work?", architecture, trace, or where-is-X questions, an agent should answer in a handful of CodeGraph calls and stop — typically with **zero file reads** — rather than re-deriving the answer with `grep` + `Read`. A direct CodeGraph answer is a handful of calls; a grep/read exploration is dozens.

The MCP server delivers this guidance to agents automatically during the MCP `initialize` response.

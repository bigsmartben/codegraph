---
title: Get Started
description: Get up and running with CodeGraph in seconds.
---

Get up and running with CodeGraph in seconds.

## No Node.js required — one command grabs the right build for your OS

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/colbymchenry/codegraph/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/colbymchenry/codegraph/main/install.ps1 | iex
```

## Already have Node? Use npm instead (works on any version)

```bash
npx @colbymchenry/codegraph        # zero-install, or:
npm i -g @colbymchenry/codegraph
```

CodeGraph bundles its own runtime — nothing to compile, no native build, works the same everywhere.

## Pick a connection model

For a local single-repository workflow, run the interactive installer. It auto-configures your agent(s) — Claude Code, Cursor, Codex CLI, opencode, Hermes Agent, Gemini CLI, Antigravity IDE, Kiro.

```bash
codegraph install
```

For a team or platform workflow, use one shared HTTP MCP server instead. The Kubernetes gateway exposes `/mcp` once, fans out to many repository runtimes, and lets every agent query multiple repositories through the same remote MCP URL.

## Initialize local projects

```bash
cd your-project
codegraph init -i
```

For the local MCP path, that's it — your agent will use CodeGraph tools automatically when a `.codegraph/` directory exists. In the Kubernetes gateway path, the operator syncs and indexes each `CodeGraphRepository` instead.

## Kubernetes: one HTTP MCP URL for many repositories

For a cloud-native multi-repo setup, use the operator CRDs instead of connecting Codex to each repository runtime separately:

- Create one `CodeGraphRepository` per Git repository.
- Each repository runs its own checkout, `.codegraph` index, and HTTP MCP runtime in parallel.
- Create one `CodeGraphGateway` to expose the shared `/mcp` endpoint.
- Configure Codex, Cursor, or another MCP client with that single HTTP URL.

```bash
docker build -f deploy/operator/runtime.Containerfile -t codegraph-runtime:local .

kubectl apply -f deploy/operator/config/crd/codegraph.dev_codegraphrepositories.yaml
kubectl apply -f deploy/operator/config/crd/codegraph.dev_codegraphgateways.yaml

cd deploy/operator
go run ./cmd/manager --route-mode=ingress --runtime-image=codegraph-runtime:local
```

Apply repository resources and the gateway. For a local five-repo verification gateway:

```bash
kubectl apply -f deploy/operator/config/samples/codegraphgateway-local-verify.yaml
```

Locally, Codex can connect through a single URL:

```toml
[mcp_servers.codegraph_k8s]
url = "http://127.0.0.1/mcp"
enabled = true
```

Gateway tools are prefixed by repository, for example `hello-1__codegraph_explore`, so one agent session can inspect multiple repositories without switching MCP servers. See `deploy/operator/README.md` for runtime images, Git authentication, and Gateway API/Ingress routing options.

Next: build [Your First Graph](/codegraph/getting-started/your-first-graph/), or see the full [Installation](/codegraph/getting-started/installation/) options.

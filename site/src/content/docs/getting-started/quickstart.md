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

CodeGraph bundles its own runtime — nothing to compile, no native build, works the same everywhere. The interactive installer auto-configures your agent(s) — Claude Code, Cursor, Codex CLI, opencode, Hermes Agent, Gemini CLI, Antigravity IDE, Kiro.

## Initialize Projects

```bash
cd your-project
codegraph init -i
```

That's it — your agent will use CodeGraph tools automatically when a `.codegraph/` directory exists.

## Kubernetes: one MCP URL for many repositories

For a cloud-native multi-repo setup, use the operator CRDs instead of connecting Codex to each repository runtime separately.

```bash
docker build -f deploy/operator/runtime.Containerfile -t codegraph-runtime:local .

kubectl apply -f deploy/operator/config/crd/codegraph.dev_codegraphrepositories.yaml
kubectl apply -f deploy/operator/config/crd/codegraph.dev_codegraphgateways.yaml

cd deploy/operator
go run ./cmd/manager --route-mode=ingress --runtime-image=codegraph-runtime:local
```

Create one `CodeGraphRepository` per backend repo, then one `CodeGraphGateway` for the shared `/mcp` entrypoint. Locally, Codex can connect through a single URL:

```toml
[mcp_servers.codegraph_k8s]
url = "http://127.0.0.1/mcp"
enabled = true
```

Gateway tools are prefixed by repository, for example `hello-1__codegraph_explore`. See `deploy/operator/README.md` and `deploy/operator/config/samples/codegraphgateway-local-verify.yaml` for the full Kubernetes example.

Next: build [Your First Graph](/codegraph/getting-started/your-first-graph/), or see the full [Installation](/codegraph/getting-started/installation/) options.

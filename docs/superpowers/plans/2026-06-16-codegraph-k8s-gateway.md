# CodeGraph Kubernetes MCP Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Kubernetes-deployed CodeGraph MCP Gateway that lets Codex connect to one URL while reaching at least five repository-scoped MCP runtimes.

**Architecture:** Add a TypeScript HTTP MCP gateway runtime and wire it into `codegraph serve --mcp --http --gateway-repos <json-or-file>`. Add a `CodeGraphGateway` CRD and controller resources that run the gateway with a ConfigMap of backend repo services, then expose exactly one `/mcp` route.

**Tech Stack:** TypeScript, Node HTTP server, existing MCP JSON-RPC types, Vitest, Go controller-runtime, Kubernetes ConfigMap/Deployment/Service/Ingress, Rancher Desktop K8s.

---

## File Structure

- Create `src/mcp/gateway.ts`: standalone HTTP MCP gateway implementation and backend forwarding logic.
- Modify `src/bin/codegraph.ts`: add gateway CLI flags and start the gateway server.
- Create `__tests__/mcp-gateway.test.ts`: fake backend servers and gateway protocol tests.
- Create `deploy/operator/api/v1alpha1/codegraphgateway_types.go`: gateway CRD spec/status.
- Modify `deploy/operator/api/v1alpha1/zz_generated.deepcopy.go` and `deploy/operator/config/crd`: generated objects/manifests.
- Create `deploy/operator/internal/resources/gateway.go` and tests: ConfigMap, Deployment, Service, Ingress/HTTPRoute builders.
- Create `deploy/operator/internal/controller/codegraphgateway_controller.go` and tests.
- Modify `deploy/operator/cmd/manager/main.go`: register the gateway controller.
- Modify `deploy/operator/README.md` and samples: single URL and Codex `config.toml`.

## Tasks

- [ ] Write failing Vitest coverage for gateway initialize, tools aggregation, dispatch, and unknown prefixes.
- [ ] Implement the minimal TypeScript gateway runtime until the new tests pass.
- [ ] Add CLI flags for `codegraph serve --mcp --http --gateway-repos <json-or-file>` and cover startup through tests or build.
- [ ] Write failing Go tests for `CodeGraphGateway` API helpers and scheme registration.
- [ ] Implement gateway API types, regenerate deepcopy and CRD manifests.
- [ ] Write failing Go tests for gateway resource builders.
- [ ] Implement ConfigMap, Deployment, Service, and route builders for the gateway.
- [ ] Write failing Go tests for gateway reconciliation.
- [ ] Implement the gateway reconciler and wire it into the manager.
- [ ] Update operator docs and samples with the single Codex URL.
- [ ] Run `npm run build`, `npm test -- __tests__/mcp-gateway.test.ts __tests__/mcp-http.test.ts`, and `npm run test:operator`.
- [ ] Rebuild the local runtime image, apply CRDs, run the operator, create five repositories plus one gateway, and verify `http://127.0.0.1/mcp`.

## Acceptance Criteria

- Codex needs one config block only.
- `tools/list` from the gateway contains prefixed tools for all five repos.
- `tools/call` through a prefixed tool reaches the matching backend.
- Rancher Desktop verification uses local reachable IP/host `127.0.0.1`.
- Existing per-repository `/mcp/<repoId>` routes are not required for Codex.

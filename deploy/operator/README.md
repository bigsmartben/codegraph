# CodeGraph Operator

The CodeGraph Operator manages `CodeGraphRepository` custom resources. Each repository CR describes one Git repository to check out, index, and serve as a remote MCP endpoint.

For each `CodeGraphRepository`, the operator owns:

- A checkout/index PVC that stores the cloned repository and its `.codegraph` index.
- A sync/index Job that clones the configured Git ref and runs `codegraph init`.
- A runtime Deployment that serves the indexed repository.
- A Service that exposes the runtime pod inside the cluster.
- A route, either Gateway API `HTTPRoute` or Kubernetes `Ingress`, that exposes the MCP endpoint.

The first operator version does not run a Python proxy. The runtime Deployment reuses the existing CodeGraph HTTP MCP server directly with `codegraph serve --mcp --http`.

## MCP endpoint

All repositories share one external MCP host and use per-repository paths:

```text
https://<host>/mcp/<repoId>
```

For example, a repository with `repoId: api-service`, `host: codegraph.example.com`, and `path: /mcp/api-service` is served at:

```text
https://codegraph.example.com/mcp/api-service
```

The external route rewrites `/mcp/<repoId>` to `/mcp` before forwarding to the pod. Inside the pod, CodeGraph serves the checked-out repository from `/workspace/repo`.

## Sample repository

Apply the sample manifest after installing the CRD and starting the controller:

```sh
kubectl create namespace codegraph
kubectl apply -f config/samples/codegraphrepository.yaml
kubectl -n codegraph get codegraphrepository api-service
```

The sample uses manual sync, a 20Gi PVC, and a Git authentication secret reference.

## Git authentication

`spec.git.authSecretRef` points to an optional Kubernetes Secret in the same namespace as the `CodeGraphRepository`.

For HTTPS repositories, provide `GIT_USERNAME` and `GIT_PASSWORD` keys:

```sh
kubectl -n codegraph create secret generic api-service-git-auth \
  --from-literal=GIT_USERNAME=<username> \
  --from-literal=GIT_PASSWORD=<token>
```

For SSH repositories, provide the standard `ssh-privatekey` key:

```sh
kubectl -n codegraph create secret generic api-service-git-auth \
  --from-file=ssh-privatekey=./id_ed25519
```

The sync/index Job mounts the same secret as environment variables and as an SSH key volume. HTTPS credentials are used through `GIT_ASKPASS`; SSH credentials are used through `GIT_SSH_COMMAND`.

## Routing modes

The controller supports two routing modes:

- `gateway`: creates a Gateway API `HTTPRoute`. This is the default mode. Use `--gateway-name` and optionally `--gateway-namespace` to select the parent Gateway.
- `ingress`: creates a Kubernetes `Ingress` for the nginx ingress class.

Both modes expose the CR's `spec.mcp.host` and `spec.mcp.path`, then rewrite the external `/mcp/<repoId>` path to the pod-local `/mcp` endpoint.

## Local development

From `deploy/operator`:

```sh
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2 crd:allowDangerousTypes=true paths="./api/v1alpha1" output:crd:artifacts:config=config/crd
go test ./...
go run ./cmd/manager --route-mode=gateway --gateway-name=codegraph
```

On Windows PowerShell, install Go and ensure `go` is on `PATH`, then run the same commands with PowerShell quoting:

```powershell
go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2 crd:allowDangerousTypes=true paths="./api/v1alpha1" output:crd:artifacts:config=config/crd
go test ./...
```

Useful local checks:

```sh
kubectl apply -f config/crd
kubectl apply -f config/samples/codegraphrepository.yaml
kubectl -n codegraph describe codegraphrepository api-service
kubectl -n codegraph get pvc,job,deploy,svc
```

## Root validation

From the repository root, run the operator validation entry point with:

```sh
npm run test:operator
```

This runs only the Go operator tests under `deploy/operator`; it does not run the full TypeScript test suite.

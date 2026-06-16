# CodeGraph Operator

The CodeGraph Operator manages `CodeGraphRepository` and `CodeGraphGateway` custom resources. Repository CRs describe Git repositories to check out, index, and serve inside the cluster. Gateway CRs expose one shared external MCP endpoint that fans out to multiple repository runtime Services.

For each `CodeGraphRepository`, the operator owns:

- A checkout/index PVC that stores the cloned repository and its `.codegraph` index.
- A sync/index Job that clones the configured Git ref and runs `codegraph init`.
- A runtime Deployment that serves the indexed repository.
- A Service that exposes the runtime pod inside the cluster.

The first operator version does not run a Python proxy. The runtime Deployment reuses the existing CodeGraph HTTP MCP server directly with `codegraph serve --mcp --http`.

For each `CodeGraphGateway`, the operator owns:

- A ConfigMap containing `repos.json` for the gateway backend list.
- A Deployment that runs `codegraph serve --mcp --http --host 0.0.0.0 --port 3000 --gateway-repos /etc/codegraph-gateway/repos.json`.
- A Service on port 3000.
- A route, either Gateway API `HTTPRoute` or Kubernetes `Ingress`, that exposes the shared gateway path.

## Runtime image

The sync Job and runtime Deployment use the same CodeGraph runtime image. Build and push that image from the repository root:

```sh
docker build -f deploy/operator/runtime.Containerfile -t registry.example.com/codegraph-runtime:1.0.1 .
docker push registry.example.com/codegraph-runtime:1.0.1
```

Then either set `spec.image` on each `CodeGraphRepository`, or start the controller with a cluster-wide default:

```sh
go run ./cmd/manager --runtime-image=registry.example.com/codegraph-runtime:1.0.1
```

If neither `spec.image` nor `--runtime-image` is set, the controller marks the repository `Degraded` with reason `RuntimeImageMissing` and does not create a new PVC or sync Job. Existing runtime resources are left in place so the last healthy repository can keep serving until a valid image is restored.

## MCP endpoint

Repository runtimes listen on `/mcp` inside the cluster. A `CodeGraphGateway` exposes a single external endpoint:

```text
https://<host>/mcp
```

A gateway with `host: codegraph.example.com` and `path: /mcp` is served at:

```text
https://codegraph.example.com/mcp
```

The gateway prefixes backend tools with the configured `repoId`, and each backend URL in `repos.json` is generated as:

```text
http://<serviceName>.<namespace>.svc.cluster.local:3000/mcp
```

Repository CRs still create the in-cluster runtime Services. Use a `CodeGraphGateway` route for external access instead of exposing one address per repository.

For a local Rancher Desktop deployment exposed through `127.0.0.1`, Codex only needs one MCP server entry:

```toml
[mcp_servers.codegraph_k8s]
url = "http://127.0.0.1/mcp"
enabled = true
```

## Sample repository

Apply the sample manifest after installing the CRD and starting the controller:

```sh
kubectl create namespace codegraph
kubectl apply -f config/samples/codegraphrepository.yaml
kubectl -n codegraph get codegraphrepository api-service
```

The sample uses manual sync, a 20Gi PVC, an explicit runtime image, and a Git authentication secret reference. Replace the image with the registry tag you built and pushed.

## Sample gateway

Apply a gateway after the repository Services exist:

```sh
kubectl apply -f config/samples/codegraphgateway.yaml
kubectl -n codegraph get codegraphgateway team-gateway
```

The sample exposes `https://codegraph.example.com/mcp` and points to the `codegraph-api-service` and `codegraph-web-client` Services in the same namespace.

## Status fields

`status.resolvedRef` records the requested `spec.git.ref` observed by the latest completed sync in this first operator version. It is not a resolved commit SHA. `status.lastSyncTime` records the sync Job completion time.

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

For `CodeGraphGateway`, both modes expose `spec.host` and `spec.path` directly to the gateway Service. For `CodeGraphRepository`, the legacy route exposes `spec.mcp.host` and `spec.mcp.path`, then rewrites `/mcp/<repoId>` to the pod-local `/mcp` endpoint.

Gateway mode requires the Gateway API CRDs to be installed and a compatible `Gateway` that allows `HTTPRoute` attachment from the repository namespace. Ingress mode assumes an nginx ingress controller that honors the rewrite annotations emitted by the operator.

## Local development

From `deploy/operator`:

```sh
go run sigs.k8s.io/controller-tools/cmd/controller-gen crd:allowDangerousTypes=true paths="./api/v1alpha1" output:crd:artifacts:config=config/crd
go test ./...
go run ./cmd/manager --route-mode=gateway --gateway-name=codegraph --runtime-image=registry.example.com/codegraph-runtime:1.0.1
```

On Windows PowerShell, install Go and ensure `go` is on `PATH`, then run the same commands with PowerShell quoting:

```powershell
go run sigs.k8s.io/controller-tools/cmd/controller-gen crd:allowDangerousTypes=true paths="./api/v1alpha1" output:crd:artifacts:config=config/crd
go test ./...
```

Useful local checks:

```sh
kubectl apply -f config/crd
kubectl apply -f config/samples/codegraphrepository.yaml
kubectl apply -f config/samples/codegraphgateway.yaml
kubectl -n codegraph describe codegraphrepository api-service
kubectl -n codegraph describe codegraphgateway team-gateway
kubectl -n codegraph get pvc,job,deploy,svc,cm
```

## Root validation

From the repository root, run the operator validation entry point with:

```sh
npm run test:operator
```

This runs only the Go operator tests under `deploy/operator`; it does not run the full TypeScript test suite.

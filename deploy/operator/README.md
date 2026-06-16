# CodeGraph Operator

The CodeGraph Operator manages `CodeGraphRepository` custom resources. Each repository CR describes one Git repository to check out, index, and serve as a remote MCP endpoint.

For each `CodeGraphRepository`, the operator owns:

- A checkout/index PVC that stores the cloned repository and its `.codegraph` index.
- A sync/index Job that clones the configured Git ref and runs `codegraph init`.
- A runtime Deployment that serves the indexed repository.
- A Service that exposes the runtime pod inside the cluster.
- A route, either Gateway API `HTTPRoute` or Kubernetes `Ingress`, that exposes the MCP endpoint.

The first operator version does not run a Python proxy. The runtime Deployment reuses the existing CodeGraph HTTP MCP server directly with `codegraph serve --mcp --http`.

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

All repositories share one external MCP host and use per-repository paths:

```text
https://<host>/mcp/<repoId>
```

For example, a repository with `repoId: api-service`, `host: codegraph.example.com`, and `path: /mcp/api-service` is served at:

```text
https://codegraph.example.com/mcp/api-service
```

The external route rewrites `/mcp/<repoId>` to `/mcp` before forwarding to the pod. Inside the pod, CodeGraph serves the checked-out repository from `/workspace/repo`.
`spec.mcp.path` must equal `/mcp/<repoId>`; mismatches are rejected by the CRD and marked degraded by the controller.

## Sample repository

Apply the sample manifest after installing the CRD and starting the controller:

```sh
kubectl create namespace codegraph
kubectl apply -f config/samples/codegraphrepository.yaml
kubectl -n codegraph get codegraphrepository api-service
```

The sample uses manual sync, a 20Gi PVC, an explicit runtime image, and a Git authentication secret reference. Replace the image with the registry tag you built and pushed.

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

Both modes expose the CR's `spec.mcp.host` and `spec.mcp.path`, then rewrite the external `/mcp/<repoId>` path to the pod-local `/mcp` endpoint.

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
kubectl -n codegraph describe codegraphrepository api-service
kubectl -n codegraph get pvc,job,deploy,svc
```

## Root validation

From the repository root, run the operator validation entry point with:

```sh
npm run test:operator
```

This runs only the Go operator tests under `deploy/operator`; it does not run the full TypeScript test suite.

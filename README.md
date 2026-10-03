# trellis-proxy-sync

`trellis-proxy-sync` renders a reverse-proxy configuration from the healthy allocations of a [Trellis](https://github.com/overfold/trellis) cluster. It polls the Trellis operator API for allocations carrying a label, renders a Go template with their addresses, ports, and weights, atomically replaces the proxy's configuration file, and optionally runs a reload command.

It is an ordinary Trellis workload, not part of the orchestrator. Trellis discovers endpoints; which proxy you run, how it is configured, and how traffic is weighted are choices made here and in your templates. It lived in the Trellis repository as `orchestrator/cmd/trellis-proxy-sync` until it moved here with its history.

## How it works

Every `-interval` (default `5s`) it:

1. Lists the allocations in `TRELLIS_NAMESPACE` that match the `-label` filter, using the public Go client [`github.com/overfold/trellis/orchestrator/client`](https://github.com/overfold/trellis/tree/main/orchestrator/client) (`GET /v1/namespaces/{namespace}/allocations?label=key:value`).
2. Keeps allocations whose phase is `running`, whose health is `healthy`, and that report an address.
3. Picks one port per allocation (see [Ports](#ports)) and a weight from the `trellis/weight` label (see [Weights](#weights)).
4. Renders the template. If the output differs from the last configuration it applied, it writes the file atomically and runs `-reload-cmd`.

A failed poll, render, or write leaves the existing configuration file in place and is retried on the next tick. A failed reload is also retried: a configuration counts as applied only once the reload command succeeds. When the poll succeeds but no allocation is healthy, the template is rendered with an empty upstream list, so write the template so that an empty list still produces a configuration your proxy accepts. A render that is entirely empty is treated as unchanged and is never written, so a template should always produce some output.

## Trellis API access

Run `trellis-proxy-sync` in a Trellis task group with [`api_access`](https://github.com/overfold/trellis/blob/main/docs/public/job-specification.md). API access is always cluster-scoped; `read` is sufficient:

```yaml
api_access:
  scope: cluster
  access: read
```

Trellis then injects the environment the synchronizer reads:

| Variable | Use |
| --- | --- |
| `TRELLIS_ADDR` | API address. An address without a scheme is treated as HTTPS. |
| `TRELLIS_TOKEN` | Bearer credential. |
| `TRELLIS_NAMESPACE` | Namespace whose allocations are listed. Trellis sets it to the job's namespace. |
| `TRELLIS_CA_CERT` | Cluster CA PEM, added when TLS is configured; trusted in addition to the system roots. |

The credential has cluster-wide read authority. `TRELLIS_NAMESPACE` selects which namespace is queried; it is controller configuration, not authorization. Every task in the group receives the token, so put only reviewed images in that group. The same variables can be set by hand to run the synchronizer outside Trellis with an operator credential.

## Flags

| Flag | Required | Description |
| --- | --- | --- |
| `-label` | yes | Allocation label filter: `key:value` (for example `route:web`) or a bare key. |
| `-template` | yes | Path to the [Go `text/template`](https://pkg.go.dev/text/template) for the proxy configuration. |
| `-output` | yes | Path of the rendered configuration file. |
| `-container-port` | no | The application port to route to. See [Ports](#ports). |
| `-reload-cmd` | no | Shell command (run with `sh -c`) after each configuration change, for example `nginx -s reload`. |
| `-interval` | no | Poll interval. Default `5s`. |

## Template data

The template receives `.Upstreams`, a list of entries with:

- `.Address`: the allocation address. For namespace-networked backends this is the workload's namespace address, reachable from a proxy in the same namespace; for host-networked backends it is the node address.
- `.Port`: the port the task listens on at that address.
- `.Weight`: a positive integer, `1` unless set with `trellis/weight`.

An nginx example:

```nginx
upstream app {
{{- range .Upstreams }}
    server {{ .Address }}:{{ .Port }} weight={{ .Weight }};
{{- end }}
{{- if not .Upstreams }}
    server 127.0.0.1:1 down;
{{- end }}
}

server {
    listen 8443;
    location / {
        proxy_pass http://app;
    }
}
```

nginx rejects an empty `upstream` block, so the example emits a placeholder entry marked `down` when no backend is healthy. The configuration stays valid and requests fail with `502` until a backend is healthy again.

## Ports

Each upstream uses the port the task listens on, not a published host port: host-networked tasks listen on the node port itself, and namespace-networked tasks listen on it at their namespace address.

- With `-container-port`, an allocation that declares ports is used only if one of them is that port; an allocation that declares no ports is dialed at `-container-port` directly.
- Without `-container-port`, the allocation's first declared port is used, and allocations that declare no ports are skipped.

Pass `-container-port` whenever backends declare more than one port or none.

## Weights

`trellis-proxy-sync` reads the task-group label `trellis/weight` and passes it to the template as `.Weight`. A missing label, or a value that is not a positive integer, means weight `1`. Trellis itself does not interpret this label; it is a convention of this tool, and the template decides whether and how the proxy uses it.

Weights attach to individual allocations, so replica counts change the aggregate: four stable allocations at weight `100` and one canary at weight `5` give a `400:5` pool, not `100:5`. Sticky sessions and long-lived connections also change the observed traffic share. See [`examples/weighted-canary`](examples/weighted-canary/).

## Writing the configuration

The output is replaced atomically so the proxy never reads a partial file:

- The synchronizer writes a temporary file in the output's parent directory and renames it over the output. Give it write access to that directory, even when the output file already exists and is writable.
- The output must be a regular file or a symlink to one. Symlinks are followed and their target is replaced.
- A missing output is created with mode `0644`, subject to the process umask and the parent directory's default ACL.
- For an existing output, its owner, group, mode, and extended attributes (including ACLs and security labels) are carried over to the replacement. The process must be permitted to set that metadata, for example to `chown` to the existing owner.

## Running alongside a proxy

A typical layout runs the proxy and `trellis-proxy-sync` as two tasks in one task group, sharing the configuration through a volume, with `api_access` on that group. Keep the proxy's public listener stable: place it deliberately or put an external load balancer in front of it. The [Trellis cookbook](https://github.com/overfold/trellis/blob/main/docs/public/cookbook.md) describes the routing patterns this tool is meant for: private backends behind a namespace-networked proxy, blue/green switches, and canaries.

## Install

Download `trellis-proxy-sync_linux_x64.tar.gz` from a [release](https://github.com/overfold/trellis-proxy-sync/releases), or build from source with the Go version in [`go.mod`](go.mod):

```sh
go install github.com/overfold/trellis-proxy-sync@latest
```

The binary has no cgo dependencies; build it with `CGO_ENABLED=0` for a static binary to copy into a proxy image. Linux is the supported platform.

## Development

```sh
go test ./...
go vet ./...
golangci-lint run
CGO_ENABLED=0 go build -o bin/trellis-proxy-sync .
```

CI runs the same commands (golangci-lint `v2.12.2`) and checks that `go.mod` and `go.sum` are tidy.

### Trellis dependency

The module depends on `github.com/overfold/trellis` only for its public `orchestrator/api` and `orchestrator/client` packages. Trellis is pre-1.0 and its client changes with the wire format, so the dependency is pinned to a specific Trellis commit or release; run this tool against a Trellis cluster built from a compatible version. To move to a newer Trellis:

```sh
go get github.com/overfold/trellis@<tag-or-commit>
go mod tidy
go test ./...
```

`overfold/trellis` is public, so no `GOPRIVATE` or token configuration is needed to fetch it.

### Releases

Push a tag of the form `vX.Y.Z` on `main`. The [release workflow](.github/workflows/release.yaml) repeats the tidy, test, and vet checks, builds a static `linux/amd64` binary, and attaches `trellis-proxy-sync_linux_x64.tar.gz` to a GitHub release with generated notes. To build the same artifact locally:

```sh
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/trellis-proxy-sync .
tar -czf trellis-proxy-sync_linux_x64.tar.gz -C bin trellis-proxy-sync
```

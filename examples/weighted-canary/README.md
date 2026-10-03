# Weighted canary

**Prerequisites:** a Trellis cluster with at least five nodes where port 80 is free, and a proxy fed by `trellis-proxy-sync` whose template uses `.Weight`.

These manifests are copies of the weighted-canary fixtures in Trellis's [`examples/deployment-strategies`](https://github.com/overfold/trellis/tree/main/examples/deployment-strategies), where they are validated against the job schema. Trellis has no canary resource: a canary is a second job that shares a route label with the stable release and is weighted by the proxy.

`stable.yaml` and `canary.yaml` share `route:shop-weighted` and differ in `track` and `trellis/weight`:

```sh
trellisctl jobs apply --file examples/weighted-canary/stable.yaml
trellisctl jobs apply --file examples/weighted-canary/canary.yaml
```

Run the synchronizer for that route, with a template that consumes each upstream's weight (see [Template data](../../README.md#template-data)):

```sh
trellis-proxy-sync -label route:shop-weighted -container-port 80 \
  -template /etc/trellis-proxy-sync/nginx.conf.tmpl \
  -output /etc/nginx/conf.d/shop.conf \
  -reload-cmd 'nginx -s reload'
```

Observe errors, latency, saturation, and application-specific success metrics by release track. Increase canary exposure by changing its weight or replica count; remove it immediately with `trellisctl jobs delete shop-canary`.

Weights apply to individual allocations. Four stable replicas at weight 100 plus one canary at weight 5 produce an aggregate stable weight of 400 and canary weight of 5. Both manifests use host networking and reserve node port 80, so the five allocations need five nodes while both tracks run. Confirm the resulting percentage and load-balancer semantics, especially with sticky sessions or long-lived connections.

The fixtures use explicit nginx version tags so the release change stays readable; pin production images to immutable digests.

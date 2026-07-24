# Kodiak

Kodiak is a Kubernetes operator for stable Tailscale subnet-router workloads.
It can use either:

- an existing Tailscale-compatible control plane and auth-key Secret; or
- an externally installed Ionscale control plane managed through Kodiak
  `Tailnet` resources.

Kodiak does not install Ionscale, a DERP server, or cert-manager.

## API

- `Connector` creates a kernel-networking `StatefulSet`. Every replica has a
  stable Pod name and a dedicated Kubernetes Secret containing containerboot
  state. Kodiak advertises routes but never approves them.
- `Tailnet` manages Ionscale Tailnet policy, DNS, and feature settings.
- `AuthKey` issues user/manual enrollment credentials for a managed Tailnet.
  Connectors do not depend on `AuthKey` resources.

Managed Connectors use a `tailnetRef`. Kodiak creates a short-lived,
pre-authorized bootstrap credential for each replica, waits for containerboot
to persist the device identity, then revokes the bootstrap credential. Route
approval belongs in the Tailnet ACL `autoApprovers` policy.

External Connectors use an `authKeySecretRef`. If `loginURL` is omitted, the
Tailscale client uses its official default control plane.

## Install

The Helm chart expects Ionscale to be installed separately. Managed-control-
plane values are optional:

```yaml
managedControlPlane:
  apiEndpoint: https://ionscale-api.example.com
  loginURL: https://vpn.example.com
  adminKeySecretRef:
    name: ionscale-admin
    key: systemAdminKey
```

```sh
helm upgrade --install kodiak ./dist/chart \
  --namespace kodiak-system \
  --create-namespace \
  -f values.yaml
```

For Kustomize development installs:

```sh
make install
make deploy IMG=ghcr.io/mni-cloud/kodiak:0.2.0
```

Patch `IONSCALE_API_ENDPOINT`, `IONSCALE_LOGIN_URL`, and the optional
`ionscale-admin` Secret reference in `config/manager/manager.yaml` when using
managed Tailnets.

## External Connector

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: connector-auth
stringData:
  TS_AUTH_KEY: tskey-auth-...
---
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: Connector
metadata:
  name: office
spec:
  authKeySecretRef:
    name: connector-auth
    key: TS_AUTH_KEY
  subnetRouter:
    advertiseRoutes:
      - 10.0.1.0/24
```

For a custom control plane, add `spec.loginURL`.

## Managed Connector

```yaml
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: Connector
metadata:
  name: office
spec:
  tailnetRef:
    name: production
  tags:
    - tag:office-router
  subnetRouter:
    advertiseRoutes:
      - 10.0.1.0/24
```

The referenced Tailnet ACL must approve the route for the Connector tag.
Kodiak exposes advertised and enabled routes separately in
`status.devices[]`, so policy failures remain visible.

## Security boundary

Kodiak never calls the Ionscale route-enable API; `autoApprovers` is the only
managed route-approval authority. Connector bootstrap keys stay in
controller-owned replica State Secrets and are revoked after identity is
persisted.

Ionscale must also enforce that tags requested during registration are a
subset of the tags carried by the auth key. Until that control-plane check is
present, do not treat separation between user and Connector tags as a tenant
security boundary.

## Development

```sh
make test
helm lint dist/chart
kustomize build config/default
```

E2E tests require Docker and an isolated Kind cluster:

```sh
make test-e2e
```

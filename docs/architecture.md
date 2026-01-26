# Kodiak Architecture

## Overview

Kodiak is a Kubernetes operator for managing [ionscale](https://github.com/jsiebens/ionscale) control servers and Tailscale-based network connectivity. It provides declarative management of:

- **ControlServer**: The ionscale control plane
- **Tailnet**: Virtual networks managed by ionscale
- **AuthKey**: Authentication keys for device enrollment
- **Connector**: Tailscale subnet routers for Kubernetes network access

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                          │
│                                                                 │
│  ┌─────────────────┐    ┌─────────────────┐                    │
│  │  Kodiak         │    │  ControlServer  │                    │
│  │  Operator       │───▶│  (ionscale)     │                    │
│  │                 │    │                 │                    │
│  │  - Controllers  │    │  - API Server   │                    │
│  │  - Webhooks     │    │  - DERP Server  │                    │
│  └────────┬────────┘    │  - STUN Server  │                    │
│           │             └────────┬────────┘                    │
│           │                      │                             │
│           ▼                      ▼                             │
│  ┌─────────────────┐    ┌─────────────────┐                    │
│  │  Tailnet        │    │  Connector      │                    │
│  │  (CRD)          │───▶│  (Tailscale)    │                    │
│  │                 │    │                 │                    │
│  │  - IAM Policy   │    │  - Subnet       │                    │
│  │  - ACL Policy   │    │    Router       │                    │
│  └────────┬────────┘    │  - Routes       │                    │
│           │             └────────┬────────┘                    │
│           ▼                      │                             │
│  ┌─────────────────┐             │                             │
│  │  AuthKey        │             │                             │
│  │  (CRD)          │─────────────┘                             │
│  │                 │                                           │
│  │  - Secret       │                                           │
│  └─────────────────┘                                           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Components

### Kodiak Operator

The operator consists of four controllers and four webhooks:

#### Controllers

| Controller | Responsibility |
|------------|----------------|
| `ControlServerReconciler` | Manages ionscale Deployment, Service, PVC, ConfigMap |
| `TailnetReconciler` | Creates/updates tailnets via ionscale API |
| `AuthKeyReconciler` | Creates auth keys and stores them in Secrets |
| `ConnectorReconciler` | Deploys Tailscale containers as subnet routers |

#### Webhooks

| Webhook | Type | Purpose |
|---------|------|---------|
| `ControlServerCustomValidator` | Validating | Validates TLS config, database config |
| `ControlServerCustomDefaulter` | Mutating | Sets default listen addresses |
| `TailnetCustomValidator` | Validating | Validates required fields |
| `AuthKeyCustomValidator` | Validating | Validates expiry format |
| `AuthKeyCustomDefaulter` | Mutating | Sets default expiry (24h) |
| `ConnectorCustomValidator` | Validating | Validates auth key sources, CIDR formats |

### Custom Resource Definitions (CRDs)

#### ControlServer

Manages the ionscale control server deployment:

```yaml
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: ControlServer
spec:
  image: ghcr.io/jsiebens/ionscale:latest
  replicas: 1
  config:
    listenAddr: ":8080"
    database:
      type: sqlite|postgres
      url: "..."
      urlSecretRef: { name: ..., key: ... }
    tls:
      disable: false
      certSecretName: "..."
      acmeEnabled: true
    derp: { ... }
    dns: { ... }
    auth: { ... }
  storage:
    size: "10Gi"
```

#### Tailnet

Represents a virtual network:

```yaml
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: Tailnet
spec:
  controlServerRef:
    name: my-controlserver
  name: "production"
  iamPolicy: |
    { "groups": { ... } }
  aclPolicy: |
    { "acls": [ ... ] }
```

#### AuthKey

Authentication key for device enrollment:

```yaml
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: AuthKey
spec:
  tailnetRef:
    name: my-tailnet
  ephemeral: true
  preAuthorized: true
  expiry: "24h"
  tags: ["tag:k8s"]
```

#### Connector

Tailscale subnet router:

```yaml
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: Connector
spec:
  spec:
    tailscale:
      authKeyRef:
        name: my-authkey
      controlServerRef:
        name: my-controlserver
      advertiseRoutes:
        - "10.244.0.0/16"
```

## Data Flow

### 1. ControlServer Provisioning

```
User creates ControlServer CR
        │
        ▼
ControlServerReconciler
        │
        ├── Creates ConfigMap (ionscale config)
        ├── Creates PVC (data storage)
        ├── Creates Deployment (ionscale pod)
        ├── Creates Service (cluster access)
        └── Updates Status (endpoint, ready)
```

### 2. Tailnet Creation

```
User creates Tailnet CR
        │
        ▼
TailnetReconciler
        │
        ├── Waits for ControlServer ready
        ├── Gets admin credentials from Secret
        ├── Calls ionscale API (CreateTailnet)
        └── Updates Status (tailnetId, ready)
```

### 3. AuthKey Generation

```
User creates AuthKey CR
        │
        ▼
AuthKeyReconciler
        │
        ├── Waits for Tailnet ready
        ├── Calls ionscale API (CreateAuthKey)
        ├── Creates Secret (auth key value)
        └── Updates Status (keyId, expiresAt)
```

### 4. Connector Deployment

```
User creates Connector CR
        │
        ▼
ConnectorReconciler
        │
        ├── Resolves AuthKey → Secret
        ├── Resolves ControlServer → endpoint
        ├── Creates Deployment (tailscale container)
        ├── Enables advertised routes via API
        └── Updates Status (nodeId, tailscaleIp)
```

## Security Considerations

### Secrets Management

- Database URLs should use `urlSecretRef` instead of plain-text `url`
- OIDC client secrets use `clientSecretRef`
- Auth keys are stored in Kubernetes Secrets
- Webhook warnings alert users about insecure configurations

### TLS Configuration

- TLS is required by default (disable explicitly for development)
- Supports ACME (Let's Encrypt), pre-provisioned certificates, or cert-manager
- Webhook validates TLS configuration completeness

### Network Security

- Connectors run in userspace mode by default
- ACL policies control inter-node traffic
- Routes must be valid CIDR notation (validated by webhook)

## Deployment Topology

### Development (Single Node)

```
┌────────────────────────┐
│  Single K8s Node       │
│                        │
│  ControlServer (SQLite)│
│  Connector             │
└────────────────────────┘
```

### Production (High Availability)

```
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│  K8s Node 1     │  │  K8s Node 2     │  │  K8s Node 3     │
│                 │  │                 │  │                 │
│  ControlServer  │  │  ControlServer  │  │  Connector      │
│  (replica)      │  │  (replica)      │  │                 │
└────────┬────────┘  └────────┬────────┘  └─────────────────┘
         │                    │
         └────────┬───────────┘
                  │
         ┌────────▼────────┐
         │  PostgreSQL     │
         │  (external)     │
         └─────────────────┘
```

## Extending Kodiak

### Adding New CRDs

1. Define types in `api/v1alpha1/`
2. Generate code: `make generate manifests`
3. Implement controller in `internal/controller/`
4. Implement webhook in `internal/webhook/v1alpha1/`
5. Register in `cmd/main.go`

### Client Interface

The `ControlServerClientInterface` in `internal/client/interface.go` can be mocked for testing:

```go
type ControlServerClientInterface interface {
    CreateTailnet(ctx context.Context, request *pb.CreateTailnetRequest) (*pb.Tailnet, error)
    GetTailnet(ctx context.Context, tailnetID uint64) (*pb.Tailnet, error)
    // ... other methods
}
```

## Testing

### Unit Tests

```bash
make test
```

Tests include:
- Controller reconciliation logic (with mocked client)
- Webhook validation and defaulting
- Race condition detection (`-race` flag)

### E2E Tests

```bash
make test-e2e
```

Requires a running Kind cluster with cert-manager installed.

## References

- [ionscale](https://github.com/jsiebens/ionscale) - Open source Tailscale control server
- [Tailscale](https://tailscale.com/) - WireGuard-based mesh VPN
- [Kubebuilder](https://kubebuilder.io/) - SDK for building Kubernetes APIs

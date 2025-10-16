# kodiak

Kodiak is a Kubernetes Operator that provisions and manages a full self-hosted
Tailscale environment powered by [ionscale](https://github.com/jsiebens/ionscale).
It automates creation of the ionscale control plane, tailnets, authentication
keys, and the connector workloads that join Kubernetes networks to Tailscale.

## Description

Kodiak exposes four custom resources (`ControlServer`, `Tailnet`, `AuthKey`, and
`Connector`) that map directly to the lifecycle of an ionscale deployment:

- **ControlServer** provisions the ionscale API/control plane, including TLS
  configuration, embedded DERP options, persistent storage, and optional OIDC
  authentication. The controller renders ionscale configuration, wires any
  referenced secrets (e.g. the system admin key), and maintains a Deployment,
  Service, and PersistentVolumeClaim to run the server. Status reflects the
  service endpoint and readiness.
- **Tailnet** configures a tailnet on the control server. The controller calls
  ionscale's API to create or update tailnet state (policies, DNS, feature
  flags) and reports readiness, the assigned tailnet ID, and machine counts.
- **AuthKey** manages short-lived authentication keys for joining nodes to a
  tailnet. Kodiak requests keys from ionscale, stores the resulting value in a
  Kubernetes secret, keeps status in sync with ionscale, and rotates keys when
  specs change.
- **Connector** (existing implementation) runs a Tailscale client pod that
  connects Kubernetes networks to the tailnet, handling auth key injection,
  connector pod lifecycle, and status reporting based on `tailscale status`.

The controllers reconcile towards declarative state, use Kubernetes
finalizers to clean up remote resources (e.g. deleting tailnets or auth keys),
and surface detailed conditions in resource status.

## Getting Started

### Prerequisites
- go version v1.23.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/kodiak:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/kodiak:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/kodiak:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/kodiak/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
### Required Secrets

Control server access requires a system admin key. By default Kodiak expects a
secret named `<controlserver-name>-admin` in the same namespace with a
`systemAdminKey` entry. You can override the secret name using the annotation
`kodiak.mnicloud.jp/system-admin-key-secret` on the `ControlServer` resource.

Auth keys are stored in Kubernetes secrets. If `spec.secretName` is omitted the
controller creates a secret named `authkey-<resource-name>` containing a
`TS_AUTH_KEY` entry.

### Development Notes

- Controller unit tests use `envtest`. Install the Kubernetes control-plane
  binaries (etcd, kube-apiserver, kubectl) via `make envtest` before running
  `go test ./...`.
- E2E tests rely on Docker and Kind. Expect failures if these dependencies are
  unavailable in the current environment.

Please open issues or pull requests for bugs, enhancements, or documentation
updates.

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

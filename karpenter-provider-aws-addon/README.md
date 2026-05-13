# Karpenter Provider AWS Addon

This addon runs the Karpenter AWS provider for an OCM managed cluster. Agent
deployment is handled by the global OCM addon-manager through `AddOnTemplate`.
This controller only reconciles AWS/MSA prerequisites and the hosted
managed-cluster kubeconfig projection.

## Addon Registration

The chart and `deploy/resources` install:

- `ClusterManagementAddOn/karpenter-provider-aws`
- `AddOnTemplate/karpenter-provider-aws-default`
- `AddOnTemplate/karpenter-provider-aws-hosted`
- the prerequisite/projection controller

`ClusterManagementAddOn` is marked with
`addon.open-cluster-management.io/lifecycle: addon-manager`, supports
`addontemplates` and `addondeploymentconfigs`, and uses `Manual` install
strategy. Users create each `ManagedClusterAddOn` explicitly.

## Per-Cluster Contract

Create `AddOnDeploymentConfig/karpenter-provider-aws` in the managed-cluster
namespace. Required fields:

- `spec.agentInstallNamespace`
- customized variables `AWS_REGION`, `KARPENTER_CLUSTER_NAME`,
  `CLUSTER_ENDPOINT`, and `NODE_ROLE_ARN`

`INTERRUPTION_QUEUE` is optional.

Create `ManagedClusterAddOn/karpenter-provider-aws` in the same namespace and
reference both the selected template and the deployment config in `spec.configs`.
Use `karpenter-provider-aws-default` for Default mode and
`karpenter-provider-aws-hosted` for Hosted mode. Hosted mode also sets the
standard OCM annotation
`addon.open-cluster-management.io/hosting-cluster-name`.

Examples:

- `deploy/examples/default-managedclusteraddon.yaml`
- `deploy/examples/hosted-managedclusteraddon.yaml`

Legacy Karpenter-specific `ManagedClusterAddOn` annotations are not read and
have no fallback behavior.

## Hosted Mode

In Hosted mode, the hub Secret
`<managedClusterNamespace>/karpenter-provider-aws` must contain:

- `token`
- `ca.crt`

The controller creates a hosting-cluster `ManifestWork` that writes Secret
`karpenter-provider-aws-managed-kubeconfig` into
`spec.agentInstallNamespace`. The generated kubeconfig references
`/managed/config/token` with `tokenFile` and does not embed token material.

## AWS Identity

Default mode creates `ClusterPermission` and `AWSServiceAccountRole` for the
Karpenter pod ServiceAccount in `spec.agentInstallNamespace`.

Hosted mode creates `ManagedServiceAccount/karpenter-provider-aws`.
`ClusterPermission` binds Karpenter RBAC to that MSA subject, while
`AWSServiceAccountRole` targets the real managed-cluster ServiceAccount created
by the managed-serviceaccount addon. The hosted template runs `aws-irsa-sidecar`
and mounts the projected managed kubeconfig Secret.

Platform prerequisites remain external: `AWSWorkloadIdentityConfig/default`,
AWIO, ACK prerequisites, the MSA token issuer, and AWS infrastructure such as
SQS/EventBridge/node-role resources.

## Development

```sh
GOWORK=off go test ./...
helm lint charts/karpenter-provider-aws-addon
helm template karpenter-provider-aws-addon charts/karpenter-provider-aws-addon \
  --namespace open-cluster-management-addon
```

`hack/karpenter-sync` renders the official Karpenter chart version `1.12.1`
into the generated source files under `pkg/addon`. Review any upstream manifest
sync carefully before regenerating the static `AddOnTemplate` resources.

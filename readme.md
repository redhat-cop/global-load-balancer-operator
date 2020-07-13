
# Global Load Balancer Operator

The global-load-balancer-operator implements automation to program a DNS service to act as global load balancer for applications deployed to multiple OpenShift clusters.
This operator is designed to be deployed to a control cluster which will watch the load balanced clusters (controlled clusters).
There are two main concepts (APIs) provided by this operator:

1. GlobalDNSZone
2. GlobalDNSRecord

## GlobalDNSZone

The `GlobalDNSZone` CR allows you to configure a zone which will contain global load balanced records and the provider used to populate it.
Here is an example of GlobalDNSZone:

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: GlobalDNSZone
metadata:
  name: external-dns-zone
spec:
  # Add fields here
  domain: global.myzone.io
  provider:
    externalDNS:
      annotations:
        type: global
```

Here is a table summarizing the supported providers and their capabilities:

| Provider  | Zone Auto-Configured  | Supports Health Checks  | Supports RoundRobin LB  | Supports Proximity LB  |
|:--:|:--:|:--:|:---:|:---:|
| External-dns  | no  | no  | no  | no  |
| Route53  | yes(**)  | yes(*)  | yes(*)  | yes(*)  |

(*) only if all controlled clusters run on AWS.
(**) currently not implemented

## GlobalDNSRecord

The `GlobalDNSRecord` CR allows you to specify the intention to create a global dns record. Here is an example

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: GlobalDNSRecord
metadata:
  name: hello-global-record
spec:
  name: hello.global.myzone.io
  endpoints:
  - clusterName: cluster1
    clusterCredentialRef:
      name: kubeconfig
      namespace: cluster1
    loadBalancerServiceRef:
      name: router-default
      namespace: openshift-ingress
  - clusterName: cluster2
    clusterCredentialRef:
      name: kubeconfig
      namespace: cluster2
    loadBalancerServiceRef:
      name: router-default
      namespace: openshift-ingress
  - clusterName: cluster3
    clusterCredentialRef:
      name: kubeconfig
      namespace: cluster3
    loadBalancerServiceRef:
      name: router-default
      namespace: openshift-ingress
  ttl: 60
  loadBalancingPolicy: Multivalue
  globalZoneRef:
    name: external-dns-zone  
```

## External DNS Provider

The [external-dns]() provider delegates to external-dns the creation of the actual DNS records by creating a `DNSEndpoint` object.
The `DNSEndpoint` object will be created in the same namespace as the `GlobalDNSRecord` and will be owned by it.
The `DNSEdnpoint` object will have the same labels as the `GlobalDNSRecord` and the annotations specified in the GlobalDNSZone configuration.
External-dns should be configured to watch for DNSEnpoints at the cluster level and to point to the desired provider.
Details on configuration can be found at the external-dns git repository.
The External-dns should be used as a fall back option when other options are not available as it does not support health checks and advanced load balancing policies.

## Examples

These examples are intended to help you setting up working configuration with each of the providers

### Cluster Set up

Two approaches for cluster setup are provided

1. [One cluster, three ingress-gateways.](./docs/one-cluster-three-ingress-gateways.md) This approach is intended for development purposes and has the objective to keep resource consumption at the minimum.
2. Control cluster and three controlled clusters in different regions. This approach represents a more realistic set-up albeit it consumes more resources.

You can also set up the cluster on your own, at the end the following conditions must be met:

Three namespace `cluster1` `cluster2` `cluster3` are created.
the following environment variables are initialized for each cluster:

1. <cluster>_secret_name. Pointing to a secret in each of the cluster namespaces containing a valid kubeconfig fot that cluster
2. <cluster>_service_name.  Pointing to the name of the loadbalancer service to be used for that cluster.
3. <cluster>_service_namespace. Pointing to the namespace of the loadbalancer service to be used for that cluster.

Here are examples for the supported provider:

1. [Setting up external-dns as provider](./docs/external-dns-provider.md)
2. Setting up route53 as a provider

## Local Development

Execute the following steps to develop the functionality locally. It is recommended that development be done using a cluster with `cluster-admin` permissions.

```shell
go mod download
```

optionally:

```shell
go mod vendor
```

Using the [operator-sdk](https://github.com/operator-framework/operator-sdk), run the operator locally:

```shell
oc apply -f deploy/crds/redhatcop.redhat.io_globaldnsrecords_crd.yaml
oc apply -f deploy/crds/redhatcop.redhat.io_globaldnszones_crd.yaml
oc new-project global-load-balancer-operator
oc apply -f deploy/service_account.yaml -n global-load-balancer-operator
oc apply -f deploy/role.yaml -n global-load-balancer-operator
oc apply -f deploy/role_binding.yaml -n global-load-balancer-operator
export token=$(oc serviceaccounts get-token 'global-load-balancer-operator' -n global-load-balancer-operator)
oc login --token=${token}
OPERATOR_NAME='global-load-balancer-operator' NAMESPACE='global-load-balancer-operator' operator-sdk --verbose run local --watch-namespace "" --operator-flags="--zap-level=debug"
```


TODO:
1. <s>add a watch for DNSRecord<s>
2. test disapperance of a service: reconcicle cycle should not fail.
3. test cluster unreachable: reconcile cycle should not fail.
4. complete AWS provider
5. add ability to autodetect global routes
# Setting up azure traffic manager provider

This setup assumes you are running in AWS.

The following variables need to be initialized:

```shell
export cluster1_service_name=
export cluster2_service_name=
export cluster3_service_name=
export cluster1_service_namespace=
export cluster2_service_namespace=
export cluster3_service_namespace=
export cluster1_secret_name=
export cluster2_secret_name=
export cluster3_secret_name=
```

They will be initialized if you follow one of the installation methods

1. [one cluster, three ingresses](./one-cluster-three-ingresses.md)
2. [three clusters](./three-clusters.md)

or you can setup your onw set of clusters and initialize those variables.

## Create global zone

This will create a global zone called `global.<cluster-base-domain>` with associated zone delegation.

```shell
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export base_domain=${cluster_base_domain#*.}
export global_base_domain=global.${cluster_base_domain#*.}
export resource_group=$(oc get DNS cluster -o jsonpath='{.spec.publicZone.id}' | cut -f 5 -d "/" -)
az network dns zone create --name ${global_base_domain} --resource-group ${resource_group} --parent-name ${base_domain}
```

## Create a global DNS zone

```shell
export namespace=global-load-balancer-operator-test
oc new-project ${namespace}

export clientId=$(oc --context ${control_cluster} get secret azure-dns-global-zone-credentials -n global-load-balancer-operator -o jsonpath='{.data.azure_client_id}' | base64 -d)
export clientSecret=$(oc --context ${control_cluster} get secret azure-dns-global-zone-credentials -n global-load-balancer-operator -o jsonpath='{.data.azure_client_secret}' | base64 -d)
export tenantId=$(oc --context ${control_cluster} get secret azure-dns-global-zone-credentials -n global-load-balancer-operator -o jsonpath='{.data.azure_tenant_id}' | base64 -d)
export subscriptionId=$(oc --context ${control_cluster} get secret azure-dns-global-zone-credentials -n global-load-balancer-operator -o jsonpath='{.data.azure_subscription_id}' | base64 -d)
export resourceGroup=$(oc --context ${control_cluster} get secret azure-dns-global-zone-credentials -n global-load-balancer-operator -o jsonpath='{.data.azure_resourcegroup}' | base64 -d)
envsubst < ./global-load-balancer-operator/azure-service-account.tmpl.json > /tmp/azure.json
oc --context ${control_cluster} create secret generic azure-config-file -n global-load-balancer-operator --from-file=/tmp/azure.json

export cluster_base_domain=$(oc --context ${control_cluster} get dns cluster -o jsonpath='{.spec.baseDomain}')
export global_base_domain=global.${cluster_base_domain#*.}
export dnsResourceGroup=$(oc --context ${control_cluster} get DNS cluster -o jsonpath='{.spec.publicZone.id}' | cut -f 5 -d "/" -)
envsubst < ./docs/scripts/azure-tm-dns-zone.yaml | oc --context ${control_cluster} apply -f - -n global-load-balancer-operator
```

For provider chose

### Multivalue example

```shell
envsubst < ./docs/scripts/azure-tm-multivalue-global-dns-record.yaml | oc apply -f - -n ${namespace}
dig multivalue.${global_base_domain}
```

### Multivalue with healthcheck

```shell
envsubst < ./docs/scripts/azure-tm-multivalue-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig multivalue-hc.${global_base_domain}
```

### Geographic with healthcheck

```shell
envsubst < ./docs/scripts/azure-tm-geographic-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig geographic-hc.${global_base_domain}
```

### Performance with healthcheck

```shell
envsubst < ./docs/scripts/route53-performance-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig performance-hc.${global_base_domain}
```

## Route Autodiscovery

delete global dns records from previous examples

```shell
oc delete globaldnsrecord --all -n ${namespace}
```

create global route autodiscovery

```shell
envsubst < ./docs/scripts/azure-tm-global-route-discovery.yaml | oc apply -f - -n ${namespace}
```

You have to have created some routes that will be selected by this global route discovery.
check that global dns records are created

```shell
oc get globaldnsrecord -n ${namespace}
```

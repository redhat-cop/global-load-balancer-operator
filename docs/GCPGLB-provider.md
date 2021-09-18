# Setting up GBLGCP provider

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

1. [three clusters](./three-clusters.md)

or you can setup your onw set of clusters and initialize those variables.

## Create global zone

This will create a global zone called `global.<cluster-base-domain>` with associated zone delegation.

```shell
export cluster_base_domain=$(oc --context ${control_cluster} get dns cluster -o jsonpath='{.spec.baseDomain}')
export base_domain=${cluster_base_domain#*.}
export base_domain_zone=$(gcloud --format json dns managed-zones list --filter dnsName=${base_domain}. | jq -r .[].name)
export global_base_domain=global.${cluster_base_domain#*.}
export global_base_domain_no_dots=$(echo ${global_base_domain} | tr '.' '-')
gcloud dns managed-zones create ${global_base_domain_no_dots} --description="Raffa multicluster zone" --dns-name=${global_base_domain} --visibility=public
export ns_record_data=$(gcloud dns record-sets list -z ${global_base_domain_no_dots} --name ${global_base_domain}. --type NS | awk '(NR>1)' | awk '{print $4}')
gcloud dns record-sets create ${global_base_domain} --rrdatas=${ns_record_data} --type=NS -z ${base_domain_zone}
```

## Create a global DNS zone and record

### Create globalDNSZone

```shell
export namespace=global-load-balancer-operator-test
oc new-project ${namespace}
export namespace=global-load-balancer-operator
export cluster_base_domain=$(oc --context ${control_cluster} get dns cluster -o jsonpath='{.spec.baseDomain}')
export base_domain=${cluster_base_domain#*.}
export global_base_domain=global.${cluster_base_domain#*.}
export global_zone_name=$(echo ${global_base_domain} | tr '.' '-')
export cluster1_secret_name=$(oc --context ${control_cluster} get clusterdeployment cluster1 -n cluster1 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster2_secret_name=$(oc --context ${control_cluster} get clusterdeployment cluster2 -n cluster2 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster3_secret_name=$(oc --context ${control_cluster} get clusterdeployment cluster3 -n cluster3 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
envsubst < ./docs/scripts/glbgcp-dns-zone.yaml | oc apply -f -
```

### DNS Record example

```shell
envsubst < ./docs/scripts/glbgcp-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig example.${global_base_domain}
```

## Route Autodiscovery

delete global dns records from previous examples

```shell
oc delete globaldnsrecord --all -n ${namespace}
```

create global route autodiscovery

```shell
envsubst < ./docs/scripts/glbgcp-global-route-discovery.yaml | oc apply -f - -n ${namespace}
```

You have to have created some routes that will be selected by this global route discovery.
check that global dns records are created

```shell
oc get globaldnsrecord -n ${namespace}
```
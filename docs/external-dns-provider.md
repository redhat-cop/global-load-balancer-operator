# Setting up external-dns provider

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

# One Cluster Three Ingresses Setup
export cluster1_namespace=cluster1
export cluster2_namespace=cluster2
export cluster3_namespace=cluster3

# Three clusters setup
export CLUSTER_NAMESPACE1=name-of-acm-cluster1
export CLUSTER_NAMESPACE2=name-of-acm-cluster2
export CLUSTER_NAMESPACE3=name-of-acm-cluster3
```

They will be initialized if you follow one of the installation methods

1. [one cluster, three ingresses](./one-cluster-three-ingresses.md)
2. [three clusters](./three-clusters.md)

or you can setup your onw set of clusters and initialize those variables.

## Create global zone

This will create a global zone called `global.<cluster-base-domain>` with associate zone delegation.

```shell
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
export global_base_domain=global.${cluster_base_domain#*.}
aws route53 create-hosted-zone --name ${global_base_domain} --caller-reference $(date +"%m-%d-%y-%H-%M-%S-%N") 
export global_zone_res=$(aws route53 list-hosted-zones-by-name --dns-name ${global_base_domain} | jq -r .HostedZones[0].Id )
export global_zone_id=${global_zone_res##*/}
export delegation_record=$(aws route53 list-resource-record-sets --hosted-zone-id ${global_zone_id} | jq .ResourceRecordSets[0])
envsubst < ./docs/scripts/delegation-record.json > /tmp/delegation-record.json
aws route53 change-resource-record-sets --hosted-zone-id ${cluster_zone_id} --change-batch file:///tmp/delegation-record.json
```

## Deploy external-dns

```shell
export external_dns_namespace=external-dns
oc new-project ${external_dns_namespace}
envsubst < ./docs/scripts/external-dns-credentials.yaml | oc apply -f - -n ${external_dns_namespace}
export sguid=$(oc get project ${external_dns_namespace} -o jsonpath='{.metadata.annotations.openshift\.io/sa\.scc\.supplemental-groups}'| sed 's/\/.*//')
export uid=$(oc get project ${external_dns_namespace} -o jsonpath='{.metadata.annotations.openshift\.io/sa\.scc\.uid-range}'| sed 's/\/.*//')
export aws_key=$(oc get secret external-dns-aws-credentials -n ${external_dns_namespace} -o jsonpath='{.data.aws_secret_access_key}' | base64 -d)
export aws_id=$(oc get secret external-dns-aws-credentials -n ${external_dns_namespace} -o jsonpath='{.data.aws_access_key_id}' | base64 -d)
helm repo add bitnami https://charts.bitnami.com/bitnami
helm upgrade external-dns bitnami/external-dns --create-namespace -i -n ${external_dns_namespace} -f ./docs/scripts/external-dns-values.yaml --set txtOwnerId=external-dns --set domainFilters[0]=${global_base_domain} --set aws.credentials.secretKey=${aws_key} --set aws.credentials.accessKey=${aws_id} --set podSecurityContext.fsGroup=${sguid} --set podSecurityContext.runAsUser=${uid} --set zoneIdFilters[0]=${global_zone_id}
```

At this point install the global-dns-operator with one of the [installation methods]()

## Create a global DNS zone and record

```shell
export namespace=global-load-balancer-operator-test
oc new-project ${namespace} 
envsubst < ./docs/scripts/external-dns-zone.yaml | oc apply -f -
envsubst < ./docs/scripts/external-dns-global-dns-record-hello.yaml | oc apply -f - -n ${namespace}
envsubst < ./docs/scripts/external-dns-global-dns-record-ciao.yaml | oc apply -f - -n ${namespace}
```

## test the global dns record

```shell
dig hello.${global_base_domain}
dig ciao.${global_base_domain}
```

## Route Autodiscovery 

create global route autodiscovery

```shell
envsubst < ./docs/scripts/external-dns-global-route-discovery.yaml | oc apply -f - -n ${namespace}
```

check that global dns records are created

```shell
oc get globaldnsrecord -n ${namespace}
```

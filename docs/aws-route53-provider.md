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

## Create a global DNS zone and record

```shell
export namespace=global-load-balancer-operator-test
oc new-project ${namespace}
envsubst < ./docs/scripts/route53-credentials-request.yaml | oc apply -f - -n ${namespace}
envsubst < ./docs/scripts/route53-dns-zone.yaml | oc apply -f -
```

For provider chose

### Multivalue example

```shell
envsubst < ./docs/scripts/route53-multivalue-global-dns-record.yaml | oc apply -f - -n ${namespace}
dig multivalue.${global_base_domain}
```

### Multivalue with healthcheck

```shell
envsubst < ./docs/scripts/route53-multivalue-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig multivalue-hc.${global_base_domain}
```

### Geoproximity with healthcheck

```shell
envsubst < ./docs/scripts/route53-geoproximity-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig geoproximity-hc.${global_base_domain}
```

### Latency with healthcheck

```shell
envsubst < ./docs/scripts/route53-latency-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${namespace}
dig latency-hc.${global_base_domain}
```
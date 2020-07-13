# Testing this operator

## Preparing the Global DNS Zone in AWS

this will create a zone named `global.<base-domain>` in AWS.
it will also create a delegation record from the base-domain zone to the global zone.
it will also create the needed aws credentials for the operator to work

```shell
export namespace=global-dns
oc new-project ${namespace}
envsubst < ./test/aws-prep/credentials.yaml | oc apply -f - -n ${namespace}
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
export global_base_domain=global.${cluster_base_domain#*.}
aws route53 create-hosted-zone --name ${global_base_domain} --caller-reference $(date +"%m-%d-%y-%H-%M-%S-%N") 
export global_zone_res=$(aws route53 list-hosted-zones-by-name --dns-name ${global_base_domain} | jq -r .HostedZones[0].Id )
export global_zone_id=${global_zone_res##*/}
export delegation_record=$(aws route53 list-resource-record-sets --hosted-zone-id ${global_zone_id} | jq .ResourceRecordSets[0])
envsubst < ./test/aws-prep/delegation-record.json > /tmp/delegation-record.json
aws route53 change-resource-record-sets --hosted-zone-id ${cluster_zone_id} --change-batch file:///tmp/delegation-record.json
```

## Creating a GlobalDNSZone

```shell
envsubst < ./test/aws-test/globalDNSZone.yaml | oc apply -f - -n ${namespace}
```

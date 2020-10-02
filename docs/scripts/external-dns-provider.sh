#!/bin/bash 
set -xe 

# source env file 
source "glb_env" || exit 1

export external_dns_namespace=external-dns

if [ ! -d $HOME/global-load-balancer-operator ];
then 
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi

cd $HOME/global-load-balancer-operator

read -p "Would you like to create a new  global.${cluster_base_domain} HOSTED ZONE? " -n 1 -r
echo    # (optional) move to a new line
if [[ $REPLY =~ ^[Yy]$ ]]
then
    aws route53 create-hosted-zone --name ${global_base_domain} --caller-reference $(date +"%m-%d-%y-%H-%M-%S-%N") 
fi
export global_zone_res=$(aws route53 list-hosted-zones-by-name --dns-name ${global_base_domain} | jq -r .HostedZones[0].Id )
export global_zone_id=${global_zone_res##*/}
export delegation_record=$(aws route53 list-resource-record-sets --hosted-zone-id ${global_zone_id} | jq .ResourceRecordSets[0])
envsubst < ./docs/scripts/delegation-record.json > /tmp/delolegation-record.json
aws route53 change-resource-record-sets --hosted-zone-id ${cluster_zone_id} --change-batch file:///tmp/delegation-record.json


export external_dns_namespace=external-dns
oc new-project ${external_dns_namespace}
read -p "Would you like to use your predefined aws keys in your enviornment? " -n 1 -r
echo    # (optional) move to a new line
if [[ $REPLY =~ ^[Yy]$ ]]
then
  oc create secret generic external-dns-aws-credentials --from-literal=aws_access_key_id=${aws_id} --from-literal=aws_secret_access_key=${aws_key} 
else
  envsubst < ./docs/scripts/external-dns-credentials.yaml | oc apply -f - -n ${external_dns_namespace}
fi

export sguid=$(oc get project ${external_dns_namespace} -o jsonpath='{.metadata.annotations.openshift\.io/sa\.scc\.supplemental-groups}'| sed 's/\/.*//')
export uid=$(oc get project ${external_dns_namespace} -o jsonpath='{.metadata.annotations.openshift\.io/sa\.scc\.uid-range}'| sed 's/\/.*//')

helm repo add bitnami https://charts.bitnami.com/bitnami
helm upgrade external-dns bitnami/external-dns --create-namespace -i -n ${external_dns_namespace} -f ./docs/scripts/external-dns-values.yaml --set txtOwnerId=external-dns --set domainFilters[0]=${global_base_domain} --set aws.credentials.secretKey=${aws_key} --set aws.credentials.accessKey=${aws_id} --set podSecurityContext.fsGroup=${sguid} --set podSecurityContext.runAsUser=${uid} --set zoneIdFilters[0]=${global_zone_id}
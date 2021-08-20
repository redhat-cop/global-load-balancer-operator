#!/usr/bin/env bash

source "glb_env"



if [ ! -d $HOME/global-load-balancer-operator ];
then 
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi

cd $HOME/global-load-balancer-operator


aws sts get-caller-identity || exit $?

read -p "Would you like to create a new  global.${cluster_base_domain} HOSTED ZONE? " -n 1 -r
echo    # (optional) move to a new line
if [[ $REPLY =~ ^[Yy]$ ]]
then
    aws route53 create-hosted-zone --name ${global_base_domain} --caller-reference $(date +"%m-%d-%y-%H-%M-%S-%N") 
fi


export global_zone_res=$(aws route53 list-hosted-zones-by-name --dns-name ${global_base_domain} | jq -r .HostedZones[0].Id )
export global_zone_id=${global_zone_res##*/}
export delegation_record=$(aws route53 list-resource-record-sets --hosted-zone-id ${global_zone_id} | jq .ResourceRecordSets[0])
envsubst < ./docs/scripts/delegation-record.json > /tmp/delegation-record.json
aws route53 change-resource-record-sets --hosted-zone-id ${cluster_zone_id} --change-batch file:///tmp/delegation-record.json


export namespace=global-load-balancer-operator
oc new-project ${namespace}
$(find $HOME -type d -name "ocp-global-loadbalancer")/configure-global-load-balancer-operator.sh
read -p "Would you like to use your predefined aws keys in your enviornment? " -n 1 -r
echo    # (optional) move to a new line
if [[ $REPLY =~ ^[Yy]$ ]]
then
  oc create secret generic route53-global-zone-credentials --from-literal=aws_access_key_id=${aws_id} --from-literal=aws_secret_access_key=${aws_key} 
else
  envsubst < ./docs/scripts/route53-credentials-request.yaml | oc apply -f - -n ${namespace}
fi


envsubst < ./docs/scripts/route53-dns-zone.yaml | oc apply -f - -n ${namespace}

#echo "update secerts"
#route53-global-zone-credentials



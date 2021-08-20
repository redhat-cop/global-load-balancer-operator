#!/bin/bash 
# set -xe 

if [ ! -d $HOME/global-load-balancer-operator ];
then 
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi


cd $HOME/global-load-balancer-operator


export namespace=global-load-balancer-operator



oc delete -f deploy/crds/redhatcop.redhat.io_globaldnsrecords_crd.yaml
oc delete -f deploy/crds/redhatcop.redhat.io_globaldnszones_crd.yaml
oc delete -f deploy/crds/redhatcop.redhat.io_globalroutediscoveries_crd.yaml
oc delete -f https://raw.githubusercontent.com/kubernetes-sigs/external-dns/master/docs/contributing/crd-source/crd-manifest.yaml
oc delete -f deploy/service_account.yaml -n global-load-balancer-operator
oc delete -f deploy/role.yaml -n global-load-balancer-operator
oc delete -f deploy/role_binding.yaml -n global-load-balancer-operator
export token=$(oc serviceaccounts get-token 'global-load-balancer-operator' -n global-load-balancer-operator)
oc delete -f deploy/operator.yaml  -n global-load-balancer-operator

oc delete project ${namespace}
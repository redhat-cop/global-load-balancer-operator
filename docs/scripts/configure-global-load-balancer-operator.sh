#!/bin/bash 
#set -xe 

if [ ! -d $HOME/global-load-balancer-operator ];
then 
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi

cd $HOME/global-load-balancer-operator

oc apply -f deploy/crds/redhatcop.redhat.io_globaldnsrecords_crd.yaml
oc apply -f deploy/crds/redhatcop.redhat.io_globaldnszones_crd.yaml
oc apply -f deploy/crds/redhatcop.redhat.io_globalroutediscoveries_crd.yaml
oc apply -f https://raw.githubusercontent.com/kubernetes-sigs/external-dns/master/docs/contributing/crd-source/crd-manifest.yaml
oc new-project global-load-balancer-operator
oc apply -f deploy/service_account.yaml -n global-load-balancer-operator
oc apply -f deploy/role.yaml -n global-load-balancer-operator
oc apply -f deploy/role_binding.yaml -n global-load-balancer-operator
export token=$(oc serviceaccounts get-token 'global-load-balancer-operator' -n global-load-balancer-operator)
oc apply -f deploy/operator.yaml  -n global-load-balancer-operator

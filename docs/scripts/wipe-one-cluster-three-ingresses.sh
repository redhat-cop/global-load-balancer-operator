#!/bin/bash
#set -xe
source glb_env

if [ ! -d $HOME/global-load-balancer-operator ];
then
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi

cd $HOME/global-load-balancer-operator

for namespace in cluster1 cluster2 cluster3; do
  export namespace
  envsubst < ./docs/scripts/router.yaml | oc delete -f -
done

for namespace in cluster1 cluster2 cluster3; do
  export namespace
  oc delete project ${namespace}
done




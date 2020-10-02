#!/bin/bash
set -x 

source glb_env

if [ ! -d $HOME/global-load-balancer-operator ];
then 
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi

cd $HOME/global-load-balancer-operator

for cluster in cluster1  cluster2 cluster3; do
  case ${cluster} in

  cluster1)
    export CLUSTER_NAMESPACE=${cluster1_namespace}
    ;;

  cluster2)
    export CLUSTER_NAMESPACE=${cluster2_namespace}
    ;;

  cluster3)
    export CLUSTER_NAMESPACE=${cluster3_namespace}
    ;;

  *)
    echo "invaild flag"
    exit 1
    ;;
  esac
  echo "************************"
  echo "Wiping out ${cluster}"
  echo "************************"
  oc login --token=${ACM_HUB_TOKEN} --server=${ACM_HUB_CLUSER_URL}
  oc config use-context ${control_cluster}
  password=$(oc get secret $(oc get clusterdeployment ${CLUSTER_NAMESPACE} -n ${CLUSTER_NAMESPACE} -o jsonpath='{.spec.clusterMetadata.adminPasswordSecretRef.name}') -n ${CLUSTER_NAMESPACE} -o jsonpath='{.data.password}' | base64 -d)
  url=$(oc get clusterdeployment ${CLUSTER_NAMESPACE} -n ${CLUSTER_NAMESPACE} -o jsonpath='{.status.apiURL}')
  if [ ! -z ${url} ];
  then
    oc login -u kubeadmin -p ${password} ${url}
    export cluster_${cluster}=$(oc config current-context)
    namespace=global-loadbalancer-operator
    helm uninstall  frontend --namespace ${namespace}
    oc delete route  geoproximity-hc -n ${namespace}
    oc delete route  latency-hc  -n ${namespace}
    oc delete route multivalue -n ${namespace}
    oc delete route multivalue-hc -n ${namespace}
    oc delete ${namespace}
  fi
done
oc config use-context ${control_cluster}
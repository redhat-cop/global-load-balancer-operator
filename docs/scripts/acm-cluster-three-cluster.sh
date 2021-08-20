#!/bin/bash 

set -x 

source glb_env

if [ ! -d $HOME/global-load-balancer-operator ];
then 
    cd $HOME
    git clone https://github.com/redhat-cop/global-load-balancer-operator.git
fi

cd $HOME/global-load-balancer-operator

for cluster in cluster1 cluster2 cluster3; do
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
  echo " Configuring ${cluster}"
  echo "************************"
  oc login --token=${ACM_HUB_TOKEN} --server=${ACM_HUB_CLUSER_URL}
  oc config use-context ${control_cluster}
  url=$(oc get clusterdeployment ${CLUSTER_NAMESPACE} -n ${CLUSTER_NAMESPACE} -o jsonpath='{.status.apiURL}')
  password=$(oc get secret $(oc get clusterdeployment ${CLUSTER_NAMESPACE} -n ${CLUSTER_NAMESPACE} -o jsonpath='{.spec.clusterMetadata.adminPasswordSecretRef.name}') -n ${CLUSTER_NAMESPACE} -o jsonpath='{.data.password}' | base64 -d)

  if [ ! -z ${url} ];
  then
    oc login -u kubeadmin -p ${password} ${url}
    export cluster_${cluster}=$(oc config current-context)
    namespace=global-loadbalancer-operator
    helm repo add podinfo https://stefanprodan.github.io/podinfo
    helm upgrade --install --wait frontend --create-namespace --namespace ${namespace} --set replicaCount=2 --set backend=http://backend-podinfo:9898/echo podinfo/podinfo
    oc patch deployment frontend-podinfo -n ${namespace} -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/readinessProbe", "value":'$(yq -c . < ./docs/scripts/readiness-probe-patch.yaml)'}]' --type=json
    oc expose service frontend-podinfo --name multivalue --hostname multivalue.${global_base_domain} -l route-type=global -n ${namespace}
    oc expose service frontend-podinfo --name multivalue-hc --hostname multivalue-hc.${global_base_domain} -l route-type=global -n ${namespace}
    oc expose service frontend-podinfo --name geoproximity-hc --hostname geoproximity-hc.${global_base_domain} -l route-type=global -n ${namespace}
    oc annotate route geoproximity-hc global-load-balancer-operator.redhat-cop.io/load-balancing-policy=Geoproximity -n ${namespace}
    oc expose service frontend-podinfo --name latency-hc --hostname latency-hc.${global_base_domain} -l route-type=global -n ${namespace}
    oc annotate route latency-hc global-load-balancer-operator.redhat-cop.io/load-balancing-policy=Latency -n ${namespace}
    ## not supported yet
    ##oc expose service frontend-podinfo --name failover-hc --hostname failover-hc.${global_base_domain} -l route-type=global -n ${namespace}
    ##oc expose service frontend-podinfo --name geolocation-hc --hostname geolocation-hc.${global_base_domain} -l route-type=global -n ${namespace}
    ##oc expose service frontend-podinfo --name weighted-hc --hostname weighted-hc.${global_base_domain} -l route-type=global -n ${namespace}
  fi
done
oc config use-context ${control_cluster}
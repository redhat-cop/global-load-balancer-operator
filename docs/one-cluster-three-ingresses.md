# One Cluster Three Ingresses Setup

## Create ingress controllers

This will create three ingress controllers and relative routers. They will act as if they were ingress points for three separate clusters,
This approach is used only for testing purposes as way to to save resources, not all the possible configuration are supported with this approach (in particular global route auto discovery does not work with this approach)

```shell
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
export global_base_domain=global.${cluster_base_domain#*.}
for namespace in cluster1 cluster2 cluster3; do
  export namespace
  envsubst < ./docs/scripts/router.yaml | oc apply -f -
done
oc patch ingresscontroller default -n openshift-ingress-operator -p '{"spec": {"routeSelector": {"matchExpressions": [{"key": "route-type", "operator": "NotIn", "values": ["global"]}]}}}' --type merge
```

## Create kubeconfig secrets

This will create three kubeconfig secrets pointing to the cluster itself

```shell
oc config view -o yaml > /tmp/kubeconfig
for namespace in cluster1 cluster2 cluster3; do
  export namespace
  oc new-project ${namespace}
  oc delete secret kubeconfig -n ${namespace}
  oc create secret generic kubeconfig --from-file /tmp/kubeconfig -n ${namespace}
  export ${namespace}_secret_name=kubeconfig
done
```

## Deploy an app and a few routes in the namespaces to receive connections

```shell
helm repo add podinfo https://stefanprodan.github.io/podinfo
for namespace in cluster1 cluster2 cluster3; do
  helm upgrade --install --wait frontend --namespace ${namespace} --set replicaCount=2 --set backend=http://backend-podinfo:9898/echo podinfo/podinfo
  oc patch deployment frontend-podinfo -n ${namespace} -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/readinessProbe", "value":'$(yq -c . < ./docs/scripts/readiness-probe-patch.yaml)'}]' --type=json
  oc expose service frontend-podinfo --name multivalue --hostname multivalue.${global_base_domain} -n ${namespace} -l route-type=global,router=${namespace}
  oc expose service frontend-podinfo --name multivalue-hc --hostname multivalue-hc.${global_base_domain} -n ${namespace} -l route-type=global,router=${namespace}
done  
```

## Prepare variables for later

```shell
export cluster1_service_name=router-cluster1
export cluster2_service_name=router-cluster2
export cluster3_service_name=router-cluster3
export cluster1_service_namespace=openshift-ingress
export cluster2_service_namespace=openshift-ingress
export cluster3_service_namespace=openshift-ingress
export cluster1_secret_name=kubeconfig
export cluster2_secret_name=kubeconfig
export cluster3_secret_name=kubeconfig
```
# One Cluster Three Ingress Gateway Setup

## Create ingress controllers

This will create three ingress controllers and relative routers. They will act as if they were ingress points for three separate clusters

```shell
for namespace in cluster1 cluster2 cluster3; do
  export namespace
  envsubst < ./docs/scripts/router.yaml | oc apply -f -
done
oc patch ingresscontroller default -n openshift-ingress-operator -p '{"spec": {"labelSelector": {"matchExpressions": [{"key": "route-type", "operator": "NotIn", "values": ["global"]}]}}}' --type merge
```

## Create kubeconfig secrets

This will create three kubeconfig secrets pointing to the cluster itself

```shell
oc config view -o yaml > /tmp/kubeconfig
for namespace in cluster1 cluster2 cluster3; do
  export namespace
  oc new-project ${namespace}
  oc create secret generic kubeconfig --from-file /tmp/kubeconfig -n ${namespace}
  export ${namespace}_secret_name=kubeconfig
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
# Three clusters setup

This will install three clusters in three different AWS regions

## Set-up the clusters

### Install ACM

```shell
oc new-project open-cluster-management
oc apply -f ./docs/scripts/acm-operator.yaml -n open-cluster-management
oc create secret docker-registry acm-pull-secret --docker-server=registry.access.redhat.com/rhacm1-tech-preview --docker-username=<docker_username> --docker-password=<docker_password> -n open-cluster-management
oc apply -f ./docs/scripts/acm.yaml -n open-cluster-management
```

## Create three clusters

```shell
export ssh_key=$(cat ~/.ssh/ocp_rsa | sed 's/^/  /')
export ssh_pub_key=$(cat ~/.ssh/ocp_rsa.pub)
export pull_secret=$(cat ~/git/openshift-enablement-exam/4.0/config/pullsecret.json)
export aws_id=$(cat ~/.aws/credentials | grep aws_access_key_id | cut -d'=' -f 2)
export aws_key=$(cat ~/.aws/credentials | grep aws_secret_access_key | cut -d'=' -f 2)
export base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export base_domain=${base_domain#*.}
```

create clusters

```shell
export region="us-east-1"
envsubst < ./docs/scripts/acm-cluster-values.yaml > /tmp/values.yaml
helm upgrade cluster1 ./docs/scripts/acm-aws-cluster --create-namespace -i -n cluster1  -f /tmp/values.yaml

export region="us-east-2"
envsubst < ./docs/scripts/acm-cluster-values.yaml > /tmp/values.yaml
helm upgrade cluster2 ./docs/scripts/acm-aws-cluster --create-namespace -i -n cluster2  -f /tmp/values.yaml

export region="us-west-2"
envsubst < ./docs/scripts/acm-cluster-values.yaml > /tmp/values.yaml
helm upgrade cluster3 ./docs/scripts/acm-aws-cluster --create-namespace -i -n cluster3  -f /tmp/values.yaml
```

## Export needed variable for following steps

```shell
export cluster1_service_name=router-default
export cluster2_service_name=router-default
export cluster3_service_name=router-default
export cluster1_service_namespace=openshift-ingress
export cluster2_service_namespace=openshift-ingress
export cluster3_service_namespace=openshift-ingress
export cluster1_secret_name=$(oc get clusterdeployment cluster1 -n cluster1 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster2_secret_name=$(oc get clusterdeployment cluster2 -n cluster2 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster3_secret_name=$(oc get clusterdeployment cluster3 -n cluster3 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
```

## Login to the clusters

```shell
helm repo add podinfo https://stefanprodan.github.io/podinfo
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
export global_base_domain=global.${cluster_base_domain#*.}
export control_cluster=$(oc config current-context)
for cluster in cluster1 cluster2 cluster3; do
  oc config use-context ${control_cluster}
  password=$(oc get secret $(oc get clusterdeployment ${cluster} -n ${cluster} -o jsonpath='{.spec.clusterMetadata.adminPasswordSecretRef.name}') -n ${cluster} -o jsonpath='{.data.password}' | base64 -d)
  url=$(oc get clusterdeployment ${cluster} -n ${cluster} -o jsonpath='{.status.apiURL}')
  oc login -u kubeadmin -p ${password} ${url}
  export cluster_${cluster}=$(oc config current-context)
  namespace=global-loadbalancer-operator-test
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
done
oc config use-context ${control_cluster}
``` 
# Three clusters setup

This will install three clusters in three different AWS regions

## Set-up the clusters

### Install ACM

```shell
oc new-project open-cluster-management
oc apply -f ./docs/scripts/acm-operator.yaml -n open-cluster-management
oc create secret docker-registry acm-pull-secret --docker-server=registry.access.redhat.com/rhacm1-tech-preview --docker-username=<docker_username> --docker-password=<docker_password> -n open-cluster-management
oc apply -f ./docs/scripts/acm.yaml -n open-cluster-management
#run this to work around: https://bugzilla.redhat.com/show_bug.cgi?id=1847540
oc annotate etcdcluster etcd-cluster etcd.database.coreos.com/scope=clusterwide -n open-cluster-management
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

export region="us-west-1"
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
export cluster1_secret_name=$(oc get clusterdeployment cluster1-acm-aws-cluster -n cluster1 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster2_secret_name=$(oc get clusterdeployment cluster2-acm-aws-cluster -n cluster2 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster3_secret_name=$(oc get clusterdeployment cluster3-acm-aws-cluster -n cluster3 -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
```
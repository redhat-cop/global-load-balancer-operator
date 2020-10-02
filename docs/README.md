# global-load-balancer-operator docs 

## Requirements 
**Install helm**
```
$ curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3
$ chmod 700 get_helm.sh
$ ./get_helm.sh
```


**install yq**
```
$ curl -OL https://github.com/mikefarah/yq/releases/download/3.4.0/yq_linux_amd64
$ mv yq_linux_amd64 /usr/local/bin/yq
$ chmod +x /usr/local/bin/yq
```

**install jq**
```
sudo dnf install jq
```

## Manual Instructions  
* [Setting up external-dns provider](external-dns-provider.md)
* [Setting up Route53 provider](aws-route53-provider.md)
* [One Cluster Three Ingresses Setup](one-cluster-three-ingresses.md)
* [Three clusters setup](three-clusters.md)

## Automated Instructions  
### One Cluster Three Ingresses Setup

**Configure AWS CLI**  
Configure aws cli [configure-aws-cli.sh](https://raw.githubusercontent.com/tosin2013/openshift-4-deployment-notes/master/aws/configure-aws-cli.sh)  

**Export needed variable for following steps for One Cluster Three Ingresses Setup**  
```
cat >glb_env<<EOF
export cluster1_service_name=router-cluster1
export cluster2_service_name=router-cluster2
export cluster3_service_name=router-cluster3
export cluster1_service_namespace=openshift-ingress
export cluster2_service_namespace=openshift-ingress
export cluster3_service_namespace=openshift-ingress
export cluster1_secret_name=kubeconfig
export cluster2_secret_name=kubeconfig
export cluster3_secret_name=kubeconfig
export cluster1_namespace=cluster1
export cluster2_namespace=cluster2
export cluster3_namespace=cluster3

# You may change this if you are using a different route52 domain than the OpenShift cluster
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
export global_base_domain=global.$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')

# Get aws key from external-dns-aws-credentials or provide one
export aws_key=$(oc get secret external-dns-aws-credentials -n ${external_dns_namespace} -o jsonpath='{.data.aws_secret_access_key}' | base64 -d)
export aws_id=$(oc get secret external-dns-aws-credentials -n ${external_dns_namespace} -o jsonpath='{.data.aws_access_key_id}' | base64 -d)

# used for the external-dns-provider.sh if you would like to override it
#export aws_key="aws_secret_access_key"
#export aws_id="aws_access_key_id"

EOF
```
**Call the following scripts**  
1. `./scripts/external-dns-provider.sh`
2. `./scripts/one_cluster_three_ingresses.sh`
3. `./scripts/aws-route53-provider.sh`

#### To delete or clean up  
1. `./scripts/wipe-one-cluster-three-ingresses.sh`
2. `./scripts/wipe-aws-route53-provider.sh`
3. `./scripts/wipe-external-dns-provider.sh`

### ACM Three clusters setup

**Configure AWS CLI**  
Configure aws cli [configure-aws-cli.sh](https://raw.githubusercontent.com/tosin2013/openshift-4-deployment-notes/master/aws/configure-aws-cli.sh)  

**Export needed variable for following steps for ACM three clusters**  
```
export CLUSTER_NAMESPACE1=name-of-acm-cluster1
export CLUSTER_NAMESPACE2=name-of-acm-cluster2
export CLUSTER_NAMESPACE3=name-of-acm-cluster3
export external_dns_namespace=external-dns

cat >glb_env<<EOF
export cluster1_service_name=router-default
export cluster2_service_name=router-default
export cluster3_service_name=router-default
export cluster1_service_namespace=openshift-ingress
export cluster2_service_namespace=openshift-ingress
export cluster3_service_namespace=openshift-ingress
export control_cluster=hubcluster
export cluster1_namespace=${CLUSTER_NAMESPACE1}
export cluster2_namespace=${CLUSTER_NAMESPACE1}
export cluster3_namespace=${CLUSTER_NAMESPACE1}
export cluster1_secret_name=$(oc get clusterdeployment ${CLUSTER_NAMESPACE1} -n ${CLUSTER_NAMESPACE1} -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster2_secret_name=$(oc get clusterdeployment  ${CLUSTER_NAMESPACE2}  -n  ${CLUSTER_NAMESPACE2}  -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster3_secret_name=$(oc get clusterdeployment  ${CLUSTER_NAMESPACE3}  -n  ${CLUSTER_NAMESPACE3}  -o jsonpath='{.spec.clusterMetadata.adminKubeconfigSecretRef.name}')
export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
export global_base_domain=global.$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')

# Get aws key from external-dns-aws-credentials or provide one
export aws_key=$(oc get secret external-dns-aws-credentials -n ${external_dns_namespace} -o jsonpath='{.data.aws_secret_access_key}' | base64 -d)
export aws_id=$(oc get secret external-dns-aws-credentials -n ${external_dns_namespace} -o jsonpath='{.data.aws_access_key_id}' | base64 -d)

# used for the external-dns-provider.sh if you would like to override it
#export aws_key="aws_secret_access_key"
#export aws_id="aws_access_key_id"

# This is used for the ./scripts/acm-cluster-three-cluster.sh script
export ACM_HUB_CLUSER_URL=https://api.acm.aws.example.com:6443
export ACM_HUB_TOKEN="acm_token"
EOF
```

**Call the following scripts**  
1. `./scripts/external-dns-provider.sh`
2. `./scripts/acm-cluster-three-cluster.sh `
3. `./scripts/aws-route53-provider.sh`

#### To delete or clean up  
1. `./scripts/wipe-acm-three-cluster.sh`
2. `./scripts/wipe-aws-route53-provider.sh`
3. `./scripts/wipe-external-dns-provider.sh `

## Exercise the Global Load Balancer functions  
```
./exercise-aws-route53-provider.sh -h
Usage:
     -h|--help                  Displays this help
     -v|--verbose               Displays verbose output
    -mv|--multivalue-example             Multivalue example
    -mvhc|--multivalue-with-healthcheck                  Multivalue with healthcheck
    -geohc|--geoproximity-with-healthcheck                  Geoproximity with healthcheck
    -lathc|--latency-with-healthcheck                  Latency with healthcheck
    -rt|--route-autodiscovery                 Route Autodiscovery
    -d|--delete                 delete global dns records from previous examples
```

**Run in debug mode**  
```
DEBUG=true  ./exercise-aws-route53-provider.sh -h
```

**FLAGS that do not work on One cluster, three ingress-gateways.**  
* Multivalue with healthcheck
* Geoproximity with healthcheck
* Route Autodiscovery

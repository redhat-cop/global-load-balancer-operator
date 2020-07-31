module github.com/redhat-cop/global-load-balancer-operator

go 1.13

require (
	github.com/aws/aws-sdk-go v1.33.7
	github.com/davecgh/go-spew v1.1.1
	github.com/fatih/set v0.2.1
	github.com/openshift/api v0.0.0-20200710154525-af4dd20aed23
	github.com/openshift/cloud-credential-operator v0.0.0-20200710182712-e5d1e9c86fd8
	github.com/operator-framework/operator-sdk v0.18.1
	github.com/redhat-cop/operator-utils v0.3.1
	github.com/scylladb/go-set v1.0.2
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.18.3
	k8s.io/apimachinery v0.18.3
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/kubectl v0.18.2
	sigs.k8s.io/controller-runtime v0.6.0
	sigs.k8s.io/external-dns v0.7.2
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	github.com/Azure/go-autorest/autorest/azure/auth => github.com/Azure/go-autorest/autorest/azure/auth v0.3.0 //required by external-dns
	k8s.io/client-go => k8s.io/client-go v0.18.2 // Required by prometheus-operator
)

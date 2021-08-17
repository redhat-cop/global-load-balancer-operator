module github.com/redhat-cop/global-load-balancer-operator

go 1.16

require (
	github.com/aws/aws-sdk-go v1.36.15
	github.com/fatih/set v0.2.1
	github.com/go-logr/logr v0.4.0
	github.com/kr/pretty v0.2.1 // indirect
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/openshift/api v0.0.0-20201103184615-27004eede929
	github.com/openshift/cloud-credential-operator v0.0.0-20201217140048-e0ae005686d1
	github.com/redhat-cop/operator-utils v1.1.4
	github.com/scylladb/go-set v1.0.2
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/kubectl v0.20.2
	sigs.k8s.io/controller-runtime v0.8.3
	sigs.k8s.io/external-dns v0.7.5
)

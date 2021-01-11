module github.com/redhat-cop/global-load-balancer-operator

go 1.15

require (
	github.com/aws/aws-sdk-go v1.36.15
	github.com/fatih/set v0.2.1
	github.com/go-logr/logr v0.3.0
	github.com/gojp/goreportcard v0.0.0-20201106142952-232d912e513e // indirect
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v0.0.0-20201103184615-27004eede929
	github.com/openshift/cloud-credential-operator v0.0.0-20201217140048-e0ae005686d1
	github.com/redhat-cop/operator-utils v1.0.1
	github.com/scylladb/go-set v1.0.2
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v0.20.0
	k8s.io/kubectl v0.20.0
	sigs.k8s.io/controller-runtime v0.7.0
	sigs.k8s.io/external-dns v0.7.5
)

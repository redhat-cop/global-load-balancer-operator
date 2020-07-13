package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GlobalDNSRecordSpec defines the desired state of GlobalDNSRecord
type GlobalDNSRecordSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// Name is the fqdn that will be used for this record.
	// +kubebuilder:validation:Pattern:=`(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9][a-z0-9-]{0,61}[a-z0-9]`
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	//Enpoints is the list of the cluster endpoitns that need to be considered for this dns record
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey="clusterName"
	Endpoints []Endpoint `json:"endpoints,omitempty"`

	//TTL is the TTL for this dns record
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:60
	TTL int `json:"ttl,omitempty"`

	//Probe is the health check used to probe the health of the applications and decide which IPs to return
	//Only HttpAction is supported
	// +kubebuilder:validation:Optional
	HealthCheck v1.Probe `json:"healthCheck,omitempty"`

	// LoadBalancingPolicy describes the policy used to loadbalance the results of the DNS queries.
	// +kubebuilder:validation:Required
	// kubebuilder:validation:Enum:={"RoundRobin","Multivalue","Proximity"}
	LoadBalancingPolicy LoadBalancingPolicy `json:"loadBalancingPolicy"`

	//GlobalZoneRef represents the global zone that will be used to host this record
	// +kubebuilder:validation:Required
	GlobalZoneRef v1.LocalObjectReference `json:"globalZoneRef"`
}

// LoadBalancingPolicy describes the policy used to loadbalance the results of the DNS queries.
type LoadBalancingPolicy string

const (
	// RoundRobin means that one random IP is returned among those that are available.
	RoundRobin LoadBalancingPolicy = "RoundRobin"
	// Multivalue means that all available IPs are returned.
	Multivalue LoadBalancingPolicy = "Multivalue"
	// Proximity means that the IP that is closest to the source is returned. Typically based on historical latency.
	// If more than one record qualifies, a random one is returned.
	Proximity LoadBalancingPolicy = "Proximity"
)

// Endpoint represents a traffic ingress point to the cluster. Currently only LoadBalancer service is supported.
type Endpoint struct {
	//ClusterName name of the cluster to connect to.
	// +kubebuilder:validation:Required
	ClusterName string `json:"clusterName"`
	//CredentialsSecretRef is a reference to a secret containing the credentials to access the cluster
	//a key called "kubeconfig" containf a valid kubeconfig file for connecting to the cluster must exist in this secret.
	// +kubebuilder:validation:Required
	CredentialsSecretRef NamespacedName `json:"clusterCredentialRef"`
	//LoadBalancerServiceRef contains a reference to the load balancer service that will receive the traffic, if using a router, put here the service created by the ingress controller.
	// +kubebuilder:validation:Required
	LoadBalancerServiceRef NamespacedName `json:"loadBalancerServiceRef"`
}

// NamespacedName a pointer to a resource in a given namespace
type NamespacedName struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// GlobalDNSRecordStatus defines the observed state of GlobalDNSRecord
type GlobalDNSRecordStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalDNSRecord is the Schema for the globaldnsrecords API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=globaldnsrecords,scope=Namespaced
type GlobalDNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalDNSRecordSpec   `json:"spec,omitempty"`
	Status GlobalDNSRecordStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalDNSRecordList contains a list of GlobalDNSRecord
type GlobalDNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalDNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalDNSRecord{}, &GlobalDNSRecordList{})
}

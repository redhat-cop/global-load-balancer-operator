package v1alpha1

import (
	"github.com/operator-framework/operator-sdk/pkg/status"
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
	LoadBalancingPolicy LoadBalancingPolicy `json:"loadBalancingPolicy"`

	//GlobalZoneRef represents the global zone that will be used to host this record
	// +kubebuilder:validation:Required
	GlobalZoneRef v1.LocalObjectReference `json:"globalZoneRef"`
}

// LoadBalancingPolicy describes the policy used to loadbalance the results of the DNS queries.
// +kubebuilder:validation:Enum:={"Weighted","Multivalue","Geolocation","Geoproximity","Latency","Failover"}
type LoadBalancingPolicy string

const (
	// Multivalue means that all available IPs are returned. If a healthchekc is defined and supported by the selected provider, only the healthy IPs are returned.
	Multivalue LoadBalancingPolicy = "Multivalue"
	// Weighted means that one random IP is returned. If a healthcheck is defined and supported by the selected provider, one random IP is returned among those that are available.
	Weighted LoadBalancingPolicy = "Weighted"
	//Geolocation allows to define routing based on the geogrphy of the caller. This requires associating each endpoint with a geography....
	Geolocation LoadBalancingPolicy = "Geolocation"
	// Geoproximity means that the IP that is closest (in terms of distance) to the source is returned.
	// If more than one record qualifies, a random one is returned.
	Geoproximity LoadBalancingPolicy = "Geoproximity"
	// Latency means that the IP that is closest (in terms of measured latency) to the source is returned. Typically based on historical latency.
	// If more than one record qualifies, a random one is returned.
	Latency LoadBalancingPolicy = "Latency"
	// Failover allows you to define a primary and secodary location. It should be used for active/passive scenarios.
	Failover LoadBalancingPolicy = "Failover"
)

// Endpoint represents a traffic ingress point to the cluster. Currently only LoadBalancer service is supported.
type Endpoint struct {
	//ClusterName name of the cluster to connect to.
	// +kubebuilder:validation:Required
	ClusterName string `json:"clusterName"`
	//CredentialsSecretRef is a reference to a secret containing the credentials to access the cluster
	//a key called "kubeconfig" containing a valid kubeconfig file for connecting to the cluster must exist in this secret.
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

	// ReconcileStatus this is the general status of the main reconciler
	// +kubebuilder:validation:Optional
	Conditions status.Conditions `json:"conditions,omitempty"`

	//MonitoredServiceStatuses contains the reconcile status of each of the monitored services in the remote clusters
	// +kubebuilder:validation:Optional
	// +mapType:=granular
	MonitoredServiceStatuses map[string]status.Conditions `json:"monitoredServiceStatuses,omitempty"`

	//EndpointStatuses contains the status of the endpoint as they were looked up during the latest reconcile. We don't fail when an endpoint look up fails, but we need to tarck its status.
	// +kubebuilder:validation:Optional
	// +mapType:=granular
	EndpointStatuses map[string]status.Conditions `json:"endpointStatuses,omitempty"`

	//ProviderStatus contains provider specific status information
	// +kubebuilder:validation:Optional
	ProviderStatus ProviderStatus `json:"providerStatus,omitempty"`
}

//ProviderStatus contains provider specific status information
// Only one field can be initialized
type ProviderStatus struct {
	// +kubebuilder:validation:Optional
	Route53 *Route53ProviderStatus `json:"route53,omitempty"`
}

type Route53ProviderStatus struct {
	//PolicyID represents the route53 routing policy created for this record
	// +kubebuilder:validation:Optional
	PolicyID string `json:"policyID,omitempty"`
	//HealthCheckID represents the route53 healthcheck created for this record
	// +kubebuilder:validation:Optional
	// +mapType:=granular
	HealthCheckIDs map[string]string `json:"healthCheckID,omitempty"`
	//PolicyInstanceID represents the ID of the DNSRecord
	// +kubebuilder:validation:Optional
	PolicyInstanceID string `json:"policyInstanceID,omitempty"`
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

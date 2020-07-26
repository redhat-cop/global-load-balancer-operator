package v1alpha1

import (
	"github.com/operator-framework/operator-sdk/pkg/status"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GlobalRouteDiscoverySpec defines the desired state of GlobalRouteDiscovery
type GlobalRouteDiscoverySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// Cluster is an arrays with the list of clusters in which global routes will be discovered
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey="clusterName"
	Clusters []ClusterReference `json:"clusters,omitempty"`

	// RouteSelector is the selector that selects the global routes, this allows you to define also local routes.
	// +kubebuilder:validation:Optional
	RouteSelector metav1.LabelSelector `json:"routeSelector,omitempty"`

	//DefaultLoadBalancingPolicy defines the load balancing policy to be used by default. This can be overridden with a route annotation TODO which?
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="Multivalue"
	DefaultLoadBalancingPolicy LoadBalancingPolicy `json:"defaultLoadBalancingPolicy,omitempty"`

	//GlobalZoneRef represents the global zone that will be used to host this record
	// +kubebuilder:validation:Required
	GlobalZoneRef v1.LocalObjectReference `json:"globalZoneRef"`
}

// ClusterReference contains the infomation necessary to connect to a cluster
type ClusterReference struct {
	//ClusterName name of the cluster to connect to.
	// +kubebuilder:validation:Required
	ClusterName string `json:"clusterName"`

	//CredentialsSecretRef is a reference to a secret containing the credentials to access the cluster
	//a key called "kubeconfig" containing a valid kubeconfig file for connecting to the cluster must exist in this secret.
	// +kubebuilder:validation:Required
	CredentialsSecretRef NamespacedName `json:"clusterCredentialRef"`
}

// GlobalRouteDiscoveryStatus defines the observed state of GlobalRouteDiscovery
type GlobalRouteDiscoveryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// ReconcileStatus this is the general status of the main reconciler
	// +kubebuilder:validation:Optional
	Conditions status.Conditions `json:"conditions,omitempty"`

	//ClusterReferenceStatuses contains the status of the cluster refence connections and their latest reconcile.
	// +kubebuilder:validation:Optional
	// +mapType:=granular
	ClusterReferenceStatuses map[string]status.Conditions `json:"clusterReferenceStatuses,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalRouteDiscovery is the Schema for the globalroutediscoveries API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=globalroutediscoveries,scope=Namespaced
type GlobalRouteDiscovery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalRouteDiscoverySpec   `json:"spec,omitempty"`
	Status GlobalRouteDiscoveryStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalRouteDiscoveryList contains a list of GlobalRouteDiscovery
type GlobalRouteDiscoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalRouteDiscovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalRouteDiscovery{}, &GlobalRouteDiscoveryList{})
}

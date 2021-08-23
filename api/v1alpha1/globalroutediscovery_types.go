/*
Copyright 2020 Red Hat Community of Practice.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GlobalRouteDiscoverySpec defines the desired state of GlobalRouteDiscovery
type GlobalRouteDiscoverySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Cluster is an arrays with the list of clusters in which global routes will be discovered
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey="clusterName"
	Clusters []ClusterReference `json:"clusters,omitempty"`

	// RouteSelector is the selector that selects the global routes, this allows you to define also local routes.
	// +kubebuilder:validation:Optional
	RouteSelector metav1.LabelSelector `json:"routeSelector,omitempty"`

	//DefaultLoadBalancingPolicy defines the load balancing policy to be used by default. This can be overridden with this route annotation `global-load-balancer-operator.redhat-cop.io/load-balancing-policy`.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="Multivalue"
	// +kubebuilder:validation:Enum:={"Weighted","Multivalue","Geolocation","Geoproximity","Latency","Failover","Geographic","Performance","Subnet"}
	DefaultLoadBalancingPolicy LoadBalancingPolicy `json:"defaultLoadBalancingPolicy,omitempty"`

	//Dfeault TTL is the TTL for this dns record
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:60
	DefaultTTL int `json:"defaultTTL,omitempty"`

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

func (cr *ClusterReference) GetKey() string {
	return cr.CredentialsSecretRef.Namespace + "/" + cr.CredentialsSecretRef.Name
}

// GlobalRouteDiscoveryStatus defines the observed state of GlobalRouteDiscovery
type GlobalRouteDiscoveryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// ReconcileStatus this is the general status of the main reconciler
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	//ClusterReferenceStatuses contains the status of the cluster refence connections and their latest reconcile.
	// +kubebuilder:validation:Optional
	// +mapType:=granular
	ClusterReferenceStatuses map[string]apis.Conditions `json:"clusterReferenceStatuses,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GlobalRouteDiscovery is the Schema for the globalroutediscoveries API
type GlobalRouteDiscovery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalRouteDiscoverySpec   `json:"spec,omitempty"`
	Status GlobalRouteDiscoveryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GlobalRouteDiscoveryList contains a list of GlobalRouteDiscovery
type GlobalRouteDiscoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalRouteDiscovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalRouteDiscovery{}, &GlobalRouteDiscoveryList{})
}

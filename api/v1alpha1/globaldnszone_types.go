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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GlobalDNSZoneSpec defines the desired state of GlobalDNSZone
type GlobalDNSZoneSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Pattern:=`(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9][a-z0-9-]{0,61}[a-z0-9]`
	// +kubebuilder:validation:Required
	Domain string `json:"domain"`

	// +kubebuilder:validation:Required
	Provider ProviderConfig `json:"provider"`
}

// ProviderConfig configures kind and access to the DNS Zone.
// Exactly one of its members must be set.
type ProviderConfig struct {
	// +kubebuilder:validation:Optional
	Route53 *Route53ProviderConfig `json:"route53,omitempty"`
	// +kubebuilder:validation:Optional
	ExternalDNS *ExternalDNSProviderConfig `json:"externalDNS,omitempty"`
	// +kubebuilder:validation:Optional
	TrafficManager *TrafficManagerProviderConfig `json:"trafficManager,omitempty"`
	// +kubebuilder:validation:Optional
	GCPGLB *GCPGLBProviderConfig `json:"GCPGLB,omitempty"`
}

//ExternalDNSProviderConfig contains configuration on how configure the external DNS provider
type ExternalDNSProviderConfig struct {
	//Annotations is a map of annotations to be added to the created DNSEndpoint records.
	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations"`
}

//Route53ProviderConfig contains configuration on how to access the route53 API
type Route53ProviderConfig struct {
	//ZoneID is the AWS route53 zone ID.
	// +kubebuilder:validation:Required
	ZoneID string `json:"zoneID"`

	//CredentialsSecretRef is a reference to a secret containing the credentials to access the AWS API. The expected secret keys are "aws_access_key_id" and "aws_secret_access_key".
	// This is needed when you want to use route53 as your global load balancer but the operator does not run in an AWS cluster.
	// If the operator runs in an AWS cluster, credentials are automatically requested via a CredendialRequest object.
	// +kubebuilder:validation:Optional
	CredentialsSecretRef NamespacedName `json:"credentialsSecretRef,omitempty"`
}

//TrafficManagerProviderConfig contains configuration on how to access the Azure Traffic Manager API
type TrafficManagerProviderConfig struct {

	//CredentialsSecretRef is a reference to a secret containing the credentials to access the Azure API. The expected secret keys are "aws_access_key_id" and "aws_secret_access_key".
	// This is mandatory as the credentials minted by OCP cannot operate on traffic manager object, so it's up to you to provide credentials with enough permissions.
	// +kubebuilder:validation:Required
	CredentialsSecretRef NamespacedName `json:"credentialsSecretRef"`

	//ResourceGroup is the resource group to be used when manipulating the traffic manager profiles.
	// +kubebuilder:validation:Required
	ResourceGroup string `json:"resourceGroup"`

	//DNSZoneResourceGroup is the resource group to be used when manipulating the dns records in the global domain zone.
	// +kubebuilder:validation:Required
	DNSZoneResourceGroup string `json:"dnsZoneResourceGroup"`
}

//TrafficManagerProviderConfig contains configuration on how to access the Azure Traffic Manager API
type GCPGLBProviderConfig struct {

	//CredentialsSecretRef is a reference to a secret containing the credentials to access the gcp API.
	// This is needed when you want to use gcp glb as your global load balancer but the operator does not run in a gcp cluster.
	// If the operator runs in a gcp cluster, credentials are automatically requested via a CredendialRequest object.
	// +kubebuilder:validation:Optional
	CredentialsSecretRef NamespacedName `json:"credentialsSecretRef,omitempty"`

	//ManagedZoneName is the name of the DNS zone in which the global records are created. This must be in the same project as the clusters.
	// +kubebuilder:validation:Required
	ManagedZoneName string `json:"managedZoneName,omitempty"`
}

// GlobalDNSZoneStatus defines the observed state of GlobalDNSZone
type GlobalDNSZoneStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

func (m *GlobalDNSZone) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

func (m *GlobalDNSZone) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

// GlobalDNSZone is the Schema for the globaldnszones API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=globaldnszones,scope=Cluster
type GlobalDNSZone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalDNSZoneSpec   `json:"spec,omitempty"`
	Status GlobalDNSZoneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GlobalDNSZoneList contains a list of GlobalDNSZone
type GlobalDNSZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalDNSZone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalDNSZone{}, &GlobalDNSZoneList{})
}

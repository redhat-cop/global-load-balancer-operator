package v1alpha1

import (
	"github.com/operator-framework/operator-sdk/pkg/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GlobalDNSZoneSpec defines the desired state of GlobalDNSZone
type GlobalDNSZoneSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

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
	Route53     *Route53ProviderConfig     `json:"route53,omitempty"`
	ExternalDNS *ExternalDNSProviderConfig `json:"externalDNS,omitempty"`
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

	//CredentialsSecretRef is a reference to a secret containing the credentials to access the AWS API //TODO (content and needed permissions)
	// expected secret keys are "aws_access_key_id" and "aws_secret_access_key"
	// +kubebuilder:validation:Required
	CredentialsSecretRef NamespacedName `json:"credentialsSecretRef"`
}

// GlobalDNSZoneStatus defines the observed state of GlobalDNSZone
type GlobalDNSZoneStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Conditions status.Conditions `json:"conditions,omitempty"`
}

func (m *GlobalDNSZone) GetReconcileStatus() status.Conditions {
	return m.Status.Conditions
}

func (m *GlobalDNSZone) SetReconcileStatus(reconcileStatus status.Conditions) {
	m.Status.Conditions = reconcileStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalDNSZone is the Schema for the globaldnszones API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=globaldnszones,scope=Cluster
type GlobalDNSZone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalDNSZoneSpec   `json:"spec,omitempty"`
	Status GlobalDNSZoneStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GlobalDNSZoneList contains a list of GlobalDNSZone
type GlobalDNSZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalDNSZone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalDNSZone{}, &GlobalDNSZoneList{})
}

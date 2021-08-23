package azure

import (
	"context"
	"errors"
	"os"
	"reflect"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/dns/mgmt/dns"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/trafficmanager/mgmt/trafficmanager"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const credentialsName = "global-load-balancer-operator-infra-credentials"
const credentialsNamespace = "openshift-cloud-credential-operator"
const credentialsSecretName = "global-load-balancer-operator-infra-credentials"

var log = logf.Log.WithName("common")
var operatorNamespace string

func init() {
	on, found := os.LookupEnv("NAMESPACE")
	if !found {
		log.Error(errors.New("the \"NAMESPACE\" environment variable must be defined. It must point to the namespace in which the operator runs"), "")
		operatorNamespace = "global-load-balancer-operator"
		return
	}
	operatorNamespace = on
}

type AzureClients struct {
	subscription string
	authorizer   autorest.Authorizer
}

func (ac *AzureClients) GetEndpointsClient() trafficmanager.EndpointsClient {
	endpointsClient := trafficmanager.NewEndpointsClient(ac.subscription)
	endpointsClient.Authorizer = ac.authorizer
	return endpointsClient
}

func (ac *AzureClients) GetProfilesClient() trafficmanager.ProfilesClient {
	profilesClient := trafficmanager.NewProfilesClient(ac.subscription)
	profilesClient.Authorizer = ac.authorizer
	return profilesClient
}

func (ac *AzureClients) GetPublicIPAddressesClient() network.PublicIPAddressesClient {
	publicIPClient := network.NewPublicIPAddressesClient(ac.subscription)
	publicIPClient.Authorizer = ac.authorizer
	return publicIPClient
}

func (ac *AzureClients) GetDNSClient() dns.RecordSetsClient {
	recordSetsClient := dns.NewRecordSetsClient(ac.subscription)
	recordSetsClient.Authorizer = ac.authorizer
	return recordSetsClient
}

func GetAzureClient(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*AzureClients, error) {
	err := ensureCredentialsRequestExists(context, r)
	if err != nil {
		log.Error(err, "unable to create CredentialRequest for azure")
		return nil, err
	}
	clientID, clientSecret, tenantID, subscription, err := getAzureCredentials(context, instance, r)
	if err != nil {
		log.Error(err, "unable to get azure credentials")
		return nil, err
	}
	authorizer, err := getAuthorizer(clientID, clientSecret, tenantID)
	if err != nil {
		log.Error(err, "unable to get azure authorizer")
		return nil, err
	}

	return &AzureClients{
		subscription: subscription,
		authorizer:   authorizer,
	}, nil
}

func getAuthorizer(clientID string, clientSecret string, tenantID string) (autorest.Authorizer, error) {
	oauthConfig, err := adal.NewOAuthConfig(azure.PublicCloud.ActiveDirectoryEndpoint, tenantID)
	if err != nil {
		return nil, err
	}
	spToken, err := adal.NewServicePrincipalToken(*oauthConfig, clientID, clientSecret, azure.PublicCloud.ResourceManagerEndpoint)
	if err != nil {
		return nil, err
	}
	return autorest.NewBearerAuthorizer(spToken), nil
}

func getAzureCredentials(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (clientID string, clientSecret string, tenantID string, subscription string, err error) {

	azureCredentialSecret, err := getAzureCredentialSecret(context, instance, r)
	if err != nil {
		log.Error(err, "unable to get credential secret")
		return "", "", "", "", err
	}

	clientIDb, ok := azureCredentialSecret.Data["azure_client_id"]
	if !ok {
		err := errors.New("unable to find key azure_client_id in secret " + azureCredentialSecret.String())
		log.Error(err, "")
		return "", "", "", "", err
	}

	clientSecretb, ok := azureCredentialSecret.Data["azure_client_secret"]
	if !ok {
		err := errors.New("unable to find key azure_client_secret in secret " + azureCredentialSecret.String())
		log.Error(err, "")
		return "", "", "", "", err
	}

	tenantIDb, ok := azureCredentialSecret.Data["azure_tenant_id"]
	if !ok {
		err := errors.New("unable to find key azure_tenant_id in secret " + azureCredentialSecret.String())
		log.Error(err, "")
		return "", "", "", "", err
	}

	subscriptionb, ok := azureCredentialSecret.Data["azure_subscription_id"]
	if !ok {
		err := errors.New("unable to find key azure_subscription_id in secret " + azureCredentialSecret.String())
		log.Error(err, "")
		return "", "", "", "", err
	}

	return string(clientIDb), string(clientSecretb), string(tenantIDb), string(subscriptionb), nil
}

func getAzureCredentialSecret(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*corev1.Secret, error) {
	credentialSecret := &corev1.Secret{}
	var secret_name, secret_namespace string
	if instance.Spec.Provider.TrafficManager.CredentialsSecretRef.Name != "" {
		secret_name = instance.Spec.Provider.TrafficManager.CredentialsSecretRef.Name
		secret_namespace = instance.Spec.Provider.TrafficManager.CredentialsSecretRef.Namespace
	} else {
		secret_name = credentialsSecretName
		secret_namespace = operatorNamespace
	}
	err := r.GetClient().Get(context, types.NamespacedName{
		Name:      secret_name,
		Namespace: secret_namespace,
	}, credentialSecret)
	if err != nil {
		log.Error(err, "unable to retrive azure credential ", "secret", types.NamespacedName{
			Name:      secret_name,
			Namespace: secret_namespace,
		})
		return &corev1.Secret{}, err
	}
	return credentialSecret, nil
}

func ensureCredentialsRequestExists(context context.Context, r *util.ReconcilerBase) error {

	credentialRequest := &cloudcredentialv1.CredentialsRequest{}
	err := r.GetClient().Get(context, types.NamespacedName{
		Name:      credentialsName,
		Namespace: credentialsNamespace,
	}, credentialRequest)

	if err != nil {
		if apierrors.IsNotFound(err) {
			credentialRequest = getAzureCredentialRequest()
			err := r.GetClient().Create(context, credentialRequest, &client.CreateOptions{})
			if err != nil {
				log.Error(err, "unable to create", "credentials request", credentialRequest)
				return err
			}
		} else {
			log.Error(err, "unable to lookup", "credential request", types.NamespacedName{
				Name:      credentialsName,
				Namespace: credentialsNamespace,
			})
			return err
		}

	}
	desiredCredentialRequest := getAzureCredentialRequest()
	if !reflect.DeepEqual(credentialRequest.Spec, desiredCredentialRequest.Spec) {
		credentialRequest.Spec = desiredCredentialRequest.Spec
		err := r.GetClient().Update(context, credentialRequest, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update ", "credentials request", credentialRequest)
			return err
		}
	}
	return nil
}

func getAzureCredentialRequest() *cloudcredentialv1.CredentialsRequest {
	azureSpec := cloudcredentialv1.AzureProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "AzureProviderSpec",
		},
		RoleBindings: []cloudcredentialv1.RoleBinding{
			{Role: "Traffic Manager Contributor"},
			{Role: "DNS Zone Contributor"},
			{Role: "Network Contributor"}},
	}
	request := cloudcredentialv1.CredentialsRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credentialsName,
			Namespace: credentialsNamespace,
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "CredentialsRequest",
		},
		Spec: cloudcredentialv1.CredentialsRequestSpec{
			SecretRef: corev1.ObjectReference{
				Name:      credentialsSecretName,
				Namespace: operatorNamespace,
			},
			ProviderSpec: &runtime.RawExtension{
				Object: &azureSpec,
			},
		},
	}
	return &request
}

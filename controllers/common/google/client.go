package azure

import (
	"context"
	"errors"
	"os"
	"reflect"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
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

func GetGCPClients(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*compute.Service, *dns.Service, error) {
	err := ensureCredentialsRequestExists(context, r)
	if err != nil {
		log.Error(err, "unable to create CredentialRequest for gcp")
		return nil, nil, err
	}
	jsonSecret, err := getGCPCredentials(context, instance, r)
	if err != nil {
		log.Error(err, "unable to get gcp credentials")
		return nil, nil, err
	}
	creds, err := google.CredentialsFromJSON(context, []byte(jsonSecret), secretmanager.DefaultAuthScopes()...)
	if err != nil {
		log.Error(err, "unable to create gcp creds from json")
		return nil, nil, err
	}
	computeClient, err := compute.NewService(context, option.WithCredentials(creds))
	if err != nil {
		log.Error(err, "unable to create compute client")
		return nil, nil, err
	}
	dnsClient, err := dns.NewService(context, option.WithCredentials(creds))
	if err != nil {
		log.Error(err, "unable to create dns client")
		return nil, nil, err
	}
	return computeClient, dnsClient, nil
}

func getGCPCredentials(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (string, error) {

	gcpCredentialSecret, err := getGCPCredentialSecret(context, instance, r)
	if err != nil {
		log.Error(err, "unable to get credential secret")
		return "", err
	}

	json, ok := gcpCredentialSecret.Data["service_account.json"]
	if !ok {
		err := errors.New("unable to find key service_account.json in secret " + gcpCredentialSecret.String())
		log.Error(err, "")
		return "", err
	}

	return string(json), nil
}

func getGCPCredentialSecret(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*corev1.Secret, error) {
	credentialSecret := &corev1.Secret{}
	var secret_name, secret_namespace string
	if instance.Spec.Provider.GCPGLB.CredentialsSecretRef.Name != "" {
		secret_name = instance.Spec.Provider.GCPGLB.CredentialsSecretRef.Name
		secret_namespace = instance.Spec.Provider.GCPGLB.CredentialsSecretRef.Namespace
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
			credentialRequest = getGCPCredentialRequest()
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
	desiredCredentialRequest := getGCPCredentialRequest()
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

func getGCPCredentialRequest() *cloudcredentialv1.CredentialsRequest {
	gcpSpec := cloudcredentialv1.GCPProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "GCPProviderSpec",
		},
		PredefinedRoles: []string{},
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
				Object: &gcpSpec,
			},
		},
	}
	return &request
}

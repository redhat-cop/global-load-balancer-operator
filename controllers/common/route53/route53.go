package route53

import (
	"context"
	"errors"
	"os"
	"reflect"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
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

func GetRoute53Client(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*route53.Route53, error) {
	err := ensureCredentialsRequestExists(context, r)
	if err != nil {
		log.Error(err, "unable to create CredentialRequest for route53")
		return nil, err
	}
	id, key, err := getAWSCredentials(context, instance, r)
	if err != nil {
		log.Error(err, "unable to get aws credentials")
		return nil, err
	}
	mySession := session.Must(session.NewSession())
	client := route53.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")))
	return client, nil
}

func getAWSCredentials(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (id string, key string, err error) {
	awsCredentialSecret, err := getAWSCredentialSecret(context, instance, r)
	if err != nil {
		log.Error(err, "unable to get credential secret")
		return "", "", err
	}

	// aws_access_key_id: QUtJQVRKVjUyWVhTV1dTRFhQTEI=
	// aws_secret_access_key: ZzlTQzR1VEd5YUV5ejhRZXVCYnMzOTgzZDlEQ216K1NESjJFVFNTYQ==

	awsAccessKeyID, ok := awsCredentialSecret.Data["aws_access_key_id"]
	if !ok {
		err := errors.New("unable to find key aws_access_key_id in secret " + awsCredentialSecret.String())
		log.Error(err, "")
		return "", "", err
	}

	awsSecretAccessKey, ok := awsCredentialSecret.Data["aws_secret_access_key"]
	if !ok {
		err := errors.New("unable to find key aws_secret_access_key in secret " + awsCredentialSecret.String())
		log.Error(err, "")
		return "", "", err
	}

	return string(awsAccessKeyID), string(awsSecretAccessKey), nil
}

func getAWSCredentialSecret(context context.Context, instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*corev1.Secret, error) {
	credentialSecret := &corev1.Secret{}
	var secret_name, secret_namespace string
	if instance.Spec.Provider.Route53.CredentialsSecretRef.Name != "" {
		secret_name = instance.Spec.Provider.Route53.CredentialsSecretRef.Name
		secret_namespace = instance.Spec.Provider.Route53.CredentialsSecretRef.Namespace
	} else {
		secret_name = credentialsSecretName
		secret_namespace = operatorNamespace
	}
	err := r.GetClient().Get(context, types.NamespacedName{
		Name:      secret_name,
		Namespace: secret_namespace,
	}, credentialSecret)
	if err != nil {
		log.Error(err, "unable to retrive aws credential ", "secret", types.NamespacedName{
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
			credentialRequest = getAWSCredentialRequest()
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
	desiredCredentialRequest := getAWSCredentialRequest()
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

func getAWSCredentialRequest() *cloudcredentialv1.CredentialsRequest {
	awsSpec := cloudcredentialv1.AWSProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "AWSProviderSpec",
		},
		StatementEntries: []cloudcredentialv1.StatementEntry{
			{
				Action: []string{
					"route53:GetHostedZone",
					"route53:CreateTrafficPolicy",
					"route53:DeleteTrafficPolicy",
					"route53:GetTrafficPolicy",
					"route53:ListTrafficPolicies",
					"route53:CreateTrafficPolicyInstance",
					"route53:DeleteTrafficPolicyInstance",
					"route53:GetTrafficPolicyInstance",
					"route53:ListTrafficPolicyInstancesByHostedZone",
					"route53:ListTrafficPolicyInstancesByPolicy",
					"route53:ListHealthChecks",
					"route53:GetHealthCheck",
					"route53:UpdateHealthCheck",
					"route53:DeleteHealthCheck",
					"route53:CreateHealthCheck",
					"route53:ChangeTagsForResource",
				},
				Effect:   "Allow",
				Resource: "*",
			},
		},
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
				Object: &awsSpec,
			},
		},
	}
	return &request
}

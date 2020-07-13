package globaldnsrecord

import (
	"context"
	errs "errors"
	"reflect"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const operatorNamespace = "global-load-balancer-operator"
const credentialsName = "global-load-balancer-operator-infra-credentials"
const credentialsNamespace = "openshift-cloud-credential-operator"
const credentialsSecretName = "global-load-balancer-operator-infra-credentials"

func (ep *EndpointStatus) loadELBConfig() error {
	//verify/create global-dns-namespace exists
	err := ep.ensureOperatorNamespaceExists()
	if err != nil {
		log.Error(err, "unable to ensure existance of", "namespace", operatorNamespace, "for endpoint", ep.endpoint)
		return err
	}

	//verify/create credential request
	err = ep.ensureCredentialsRequestExists()
	if err != nil {
		log.Error(err, "unable to ensure existance of", "credentials request", types.NamespacedName{
			Name:      credentialsSecretName,
			Namespace: credentialsNamespace,
		}, "for endpoint", ep.endpoint)
		return err
	}

	//verify secret exists

	credentialSecret, err := ep.getAWSCredentialSecret()
	if err != nil {
		log.Error(err, "unable to lookup", "credential secret", types.NamespacedName{
			Name:      credentialsSecretName,
			Namespace: credentialsNamespace,
		}, "for endpoint", ep.endpoint)
		return err
	}

	id, key, err := getAWSCredentials(&credentialSecret)

	if err != nil {
		log.Error(err, "unable to find credentials in", "credential secret", credentialSecret, "for endpoint", ep.endpoint)
		return err
	}

	//create aws client

	mySession := session.Must(session.NewSession())
	_ = ec2.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")).WithRegion(ep.infrastructure.Status.PlatformStatus.AWS.Region))

	//load ELB information

	return nil
}

func (ep *EndpointStatus) getAWSCredentialSecret() (corev1.Secret, error) {
	credentialSecret := &corev1.Secret{}
	err := ep.client.Get(context.TODO(), types.NamespacedName{
		Name:      credentialsSecretName,
		Namespace: credentialsNamespace,
	}, credentialSecret)

	if err != nil {
		log.Error(err, "unable to lookup", "credential secret", types.NamespacedName{
			Name:      credentialsSecretName,
			Namespace: credentialsNamespace,
		}, "for endpoint", ep.endpoint)
		return corev1.Secret{}, err
	}
	return *credentialSecret, nil
}

func (ep *EndpointStatus) ensureCredentialsRequestExists() error {

	credentialRequest := &cloudcredentialv1.CredentialsRequest{}
	err := ep.client.Get(context.TODO(), types.NamespacedName{
		Name:      credentialsName,
		Namespace: credentialsNamespace,
	}, credentialRequest)

	if err != nil {
		if errors.IsNotFound(err) {
			credentialRequest = getAWSCredentialRequest()
			err := ep.client.Create(context.TODO(), credentialRequest, &client.CreateOptions{})
			if err != nil {
				log.Error(err, "unable to create", "credentials request", credentialRequest, "for endpoint", ep.endpoint)
				return err
			}
		} else {
			log.Error(err, "unable to lookup", "credential request", types.NamespacedName{
				Name:      credentialsName,
				Namespace: credentialsNamespace,
			}, "for endpoint", ep.endpoint)
			return err
		}

	}
	desiredCredentialRequest := getAWSCredentialRequest()
	if !reflect.DeepEqual(credentialRequest.Spec, desiredCredentialRequest.Spec) {
		credentialRequest.Spec = desiredCredentialRequest.Spec
		err := ep.client.Update(context.TODO(), credentialRequest, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update ", "credentials request", credentialRequest, "for endpoint", ep.endpoint)
			return err
		}
	}
	return nil
}

func (ep *EndpointStatus) ensureOperatorNamespaceExists() error {
	namespace := &corev1.Namespace{}
	err := ep.client.Get(context.TODO(), types.NamespacedName{
		Name: operatorNamespace,
	}, namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			//create the namespace
			namespace.Name = operatorNamespace
			err := ep.client.Create(context.TODO(), namespace, &client.CreateOptions{})
			if err != nil {
				log.Error(err, "unable to create namespace ", "namespace", operatorNamespace, "for endpoint", ep.endpoint)
				return err
			}
		} else {
			log.Error(err, "unable to lookup", "namespace", namespace, "for endpoint", ep.endpoint)
			return err
		}
	}
	return nil
}

func getAWSCredentials(credentialSecret *corev1.Secret) (id string, key string, err error) {

	// aws_access_key_id: QUtJQVRKVjUyWVhTV1dTRFhQTEI=
	// aws_secret_access_key: ZzlTQzR1VEd5YUV5ejhRZXVCYnMzOTgzZDlEQ216K1NESjJFVFNTYQ==

	awsAccessKeyID, ok := credentialSecret.Data["aws_access_key_id"]
	if !ok {
		err := errs.New("unable to find key aws_access_key_id in secret " + credentialSecret.String())
		log.Error(err, "")
		return "", "", err
	}

	awsSecretAccessKey, ok := credentialSecret.Data["aws_secret_access_key"]
	if !ok {
		err := errs.New("unable to find key aws_secret_access_key in secret " + credentialSecret.String())
		log.Error(err, "")
		return "", "", err
	}

	return string(awsAccessKeyID), string(awsSecretAccessKey), nil
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
					// "ec2:DescribeInstances",
					// "ec2:UnassignPrivateIpAddresses",
					// "ec2:AssignPrivateIpAddresses",
					// "ec2:DescribeSubnets",
					// "ec2:DescribeNetworkInterfaces",
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
				Name:      operatorNamespace,
				Namespace: operatorNamespace,
			},
			ProviderSpec: &runtime.RawExtension{
				Object: &awsSpec,
			},
		},
	}
	return &request
}

package route53

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("common")

func GetRoute53Client(instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*route53.Route53, error) {
	id, key, err := getAWSCredentials(instance, r)
	if err != nil {
		log.Error(err, "unable to get aws credentials")
		return nil, err
	}
	mySession := session.Must(session.NewSession())
	client := route53.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")))
	return client, nil
}

func getAWSCredentials(instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (id string, key string, err error) {
	awsCredentialSecret, err := getAWSCredentialSecret(instance, r)
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

func getAWSCredentialSecret(instance *redhatcopv1alpha1.GlobalDNSZone, r *util.ReconcilerBase) (*corev1.Secret, error) {
	credentialSecret := &corev1.Secret{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name:      instance.Spec.Provider.Route53.CredentialsSecretRef.Name,
		Namespace: instance.Spec.Provider.Route53.CredentialsSecretRef.Namespace,
	}, credentialSecret)
	if err != nil {
		log.Error(err, "unable to retrive aws credential ", "secret", types.NamespacedName{
			Name:      instance.Spec.Provider.Route53.CredentialsSecretRef.Name,
			Namespace: instance.Spec.Provider.Route53.CredentialsSecretRef.Namespace,
		})
		return &corev1.Secret{}, err
	}
	return credentialSecret, nil
}

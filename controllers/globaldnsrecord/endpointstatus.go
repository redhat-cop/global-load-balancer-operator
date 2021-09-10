package globaldnsrecord

import (
	"context"
	"errors"
	"net"
	"sort"

	"github.com/go-logr/logr"
	ocpconfigv1 "github.com/openshift/api/config/v1"

	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type EndpointStatus struct {
	endpoint       redhatcopv1alpha1.Endpoint
	client         client.Client
	service        corev1.Service
	infrastructure ocpconfigv1.Infrastructure
	err            error
	log            logr.Logger
}

func (es *EndpointStatus) getIPs() ([]string, error) {
	var IPs []string
	var err error
	switch es.infrastructure.Status.PlatformStatus.Type {
	case ocpconfigv1.AWSPlatformType:
		{
			es.log.V(1).Info("using getAWSIPs")
			IPs, err = es.getAWSIPs()
			if err != nil {
				return []string{}, err
			}
			break
		}
	default:
		{
			es.log.V(1).Info("using getDefaultIPs")
			IPs, err = es.getDefaultIPs()
			if err != nil {
				return []string{}, err
			}
			break
		}
	}
	sort.Strings(IPs)
	return IPs, nil
}

func (es *EndpointStatus) getDefaultIPs() ([]string, error) {
	IPs := []string{}
	for _, ingress := range es.service.Status.LoadBalancer.Ingress {
		IPs = append(IPs, ingress.IP)
	}
	return IPs, nil
}

func (es *EndpointStatus) getAWSIPs() ([]string, error) {
	IPs := []string{}
	for _, ingress := range es.service.Status.LoadBalancer.Ingress {
		addrs, err := net.LookupHost(ingress.Hostname)
		if err != nil {
			es.log.Error(err, "unable to lookup", "hostname", ingress.Hostname)
			return nil, err
		}
		IPs = append(IPs, addrs...)
	}
	return IPs, nil
}

func (r *GlobalDNSRecordReconciler) getEndPointStatus(context context.Context, endpoint redhatcopv1alpha1.Endpoint) (*EndpointStatus, error) {
	client, err := r.getClient(context, endpoint)
	if err != nil {
		r.Log.Error(err, "unable to get client for", "endpoint", endpoint)
		return nil, err
	}
	service := &corev1.Service{}
	err = client.Get(context, types.NamespacedName{
		Name:      endpoint.LoadBalancerServiceRef.Name,
		Namespace: endpoint.LoadBalancerServiceRef.Namespace,
	}, service)
	if err != nil {
		r.Log.Error(err, "unable to get service for", "endpoint", endpoint)
		return nil, err
	}

	infrastructure := &ocpconfigv1.Infrastructure{}

	err = client.Get(context, types.NamespacedName{
		Name: "cluster",
	}, infrastructure)
	if err != nil {
		r.Log.Error(err, "unable to retrieve cluster's infrastructure resource for", "endpoint", endpoint)
		return nil, err
	}

	endpointStatus := EndpointStatus{
		endpoint:       endpoint,
		client:         client,
		service:        *service,
		infrastructure: *infrastructure,
		log:            r.Log.WithName(endpoint.GetKey()),
	}

	return &endpointStatus, nil
}

func (r *GlobalDNSRecordReconciler) getRestConfig(context context.Context, endpoint redhatcopv1alpha1.Endpoint) (*rest.Config, error) {
	// for now we assume that we will have a secret with a kubeconfig.
	// the clustr ref is not really needed in this case.

	secret := &corev1.Secret{}

	err := r.GetClient().Get(context, types.NamespacedName{
		Name:      endpoint.CredentialsSecretRef.Name,
		Namespace: endpoint.CredentialsSecretRef.Namespace,
	}, secret)

	if err != nil {
		r.Log.Error(err, "unable to find", "secret", endpoint.CredentialsSecretRef)
		return nil, err
	}

	kubeconfig, ok := secret.Data["kubeconfig"]

	if !ok {
		err := errors.New("unable to find kubeconfig key in secret")
		r.Log.Error(err, "", "secret", endpoint.CredentialsSecretRef)
		return nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		r.Log.Error(err, "unable to create rest config", "kubeconfig", kubeconfig)
		return nil, err
	}

	return restConfig, nil

}

func (r *GlobalDNSRecordReconciler) getClient(context context.Context, endpoint redhatcopv1alpha1.Endpoint) (client.Client, error) {
	restConfig, err := r.getRestConfig(context, endpoint)
	if err != nil {
		r.Log.Error(err, "unable to get rest confg for ", "endpoint", err)
		return nil, err
	}

	// Create the mapper provider
	mapper, err := apiutil.NewDiscoveryRESTMapper(restConfig)
	if err != nil {
		r.Log.Error(err, "unable to create mapper", "restconfig", restConfig)
		return nil, err
	}
	scheme := scheme.Scheme
	err = ocpconfigv1.AddToScheme(scheme)
	if err != nil {
		r.Log.Error(err, "unable to add ocp config to cheme")
		return nil, err
	}
	client, err := client.New(restConfig, client.Options{
		Scheme: scheme,
		Mapper: mapper,
	})

	if err != nil {
		r.Log.Error(err, "unable to create new client")
		return nil, err
	}
	return client, nil
}

func getEndpointStatuses(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointStatusMap map[string]EndpointStatus, recorder record.EventRecorder) map[string]apis.Conditions {
	conditionMap := map[string]apis.Conditions{}
	for key, endpointStatus := range endpointStatusMap {
		if endpointStatus.err != nil {
			//we are in a error situation
			recorder.Event(instance, "Warning", "ProcessingError", endpointStatus.err.Error())
			condition := metav1.Condition{
				Type:               apis.ReconcileError,
				LastTransitionTime: metav1.Now(),
				Message:            endpointStatus.err.Error(),
				Reason:             apis.ReconcileErrorReason,
				Status:             metav1.ConditionTrue,
			}
			conditionMap[key] = apis.AddOrReplaceCondition(condition, conditionMap[key])
		} else {
			// we are in a success stuation
			condition := metav1.Condition{
				Type:               apis.ReconcileSuccess,
				LastTransitionTime: metav1.Now(),
				Reason:             apis.ReconcileSuccessReason,
				Status:             metav1.ConditionTrue,
			}
			conditionMap[key] = apis.AddOrReplaceCondition(condition, conditionMap[key])
		}
	}
	return conditionMap
}

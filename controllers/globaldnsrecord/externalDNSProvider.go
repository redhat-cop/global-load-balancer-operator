package globaldnsrecord

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/scylladb/go-set/strset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/external-dns/endpoint"
)

type externalDNSProvider struct {
	instance    *redhatcopv1alpha1.GlobalDNSRecord
	globalzone  *redhatcopv1alpha1.GlobalDNSZone
	endpointMap map[string]EndpointStatus
	log         logr.Logger
	client      client.Client
	scheme      *runtime.Scheme
}

var _ globalDNSProvider = &externalDNSProvider{}

func (r *GlobalDNSRecordReconciler) createExternalDNSProvider(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (*externalDNSProvider, error) {
	return &externalDNSProvider{
		instance:    instance,
		globalzone:  globalzone,
		endpointMap: endpointMap,
		log:         r.Log.WithName("externalDNSprovider"),
		client:      r.GetClient(),
		scheme:      r.GetScheme(),
	}, nil
}

func (p *externalDNSProvider) deleteDNSRecord(context context.Context) error {
	//no need to do anything for CR ownership
	return nil
}

func (p *externalDNSProvider) ensureDNSRecord(context context.Context) error {
	return p.createExternalDNSRecord(context)
}

func (e *externalDNSProvider) createExternalDNSRecord(context context.Context) error {
	IPs := []string{}
	for _, endpointStatus := range e.endpointMap {
		if endpointStatus.err != nil {
			continue
		}
		recordIPs, err := endpointStatus.getIPs()
		if err != nil {
			e.log.Error(err, "unale to get IPs for", "endpoint", endpointStatus)
			return err
		}
		e.log.V(1).Info("found", "IPs", recordIPs)
		IPs = append(IPs, recordIPs...)
	}
	//shake out the duplicates, althought there shouldn't be any
	IPs = strset.New(IPs...).List()
	e.log.V(1).Info("endpoint", "ips", IPs)
	newDNSEndpoint := &endpoint.DNSEndpoint{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "externaldns.k8s.io/v1alpha1",
			Kind:       "DNSEndpoint",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        e.instance.Name,
			Namespace:   e.instance.Namespace,
			Labels:      e.instance.Labels,
			Annotations: e.globalzone.Spec.Provider.ExternalDNS.Annotations,
		},
		Spec: endpoint.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    e.instance.Spec.Name,
					RecordTTL:  endpoint.TTL(e.instance.Spec.TTL),
					RecordType: endpoint.RecordTypeA,
					Targets:    IPs,
				},
			},
		},
	}
	currentDNSEndpoint := &endpoint.DNSEndpoint{}
	err := e.client.Get(context, types.NamespacedName{
		Name:      newDNSEndpoint.Name,
		Namespace: newDNSEndpoint.Namespace,
	}, currentDNSEndpoint)

	if err != nil {
		if errors.IsNotFound(err) {
			// we need to create
			err := controllerutil.SetControllerReference(e.instance, newDNSEndpoint, e.scheme)
			if err != nil {
				e.log.Error(err, "unable to set ", "controller reference", newDNSEndpoint)
				return err
			}
			err = e.client.Create(context, newDNSEndpoint, &client.CreateOptions{})
			if err != nil {
				e.log.Error(err, "unable to create", "DNSEndpoint", newDNSEndpoint)
				return err
			}
			return nil
		}
		e.log.Error(err, "unable to lookup", "DNSEndpoint", types.NamespacedName{
			Name:      newDNSEndpoint.Name,
			Namespace: newDNSEndpoint.Namespace,
		})
		return err
	}

	//workaround to deal with the array of IP changing order
	newEndpoint := newDNSEndpoint.Spec.Endpoints[0].DeepCopy()
	currentEndpoint := currentDNSEndpoint.Spec.Endpoints[0].DeepCopy()

	newIPSet := strset.New(newEndpoint.Targets...)
	currentIPSet := strset.New(currentEndpoint.Targets...)

	newEndpoint.Targets = []string{}
	currentEndpoint.Targets = []string{}

	//if we get here we possibly need to update
	if !currentIPSet.IsEqual(newIPSet) || !reflect.DeepEqual(currentEndpoint, newEndpoint) || !reflect.DeepEqual(currentDNSEndpoint.Annotations, newDNSEndpoint.Annotations) || !reflect.DeepEqual(currentDNSEndpoint.Labels, newDNSEndpoint.Labels) {
		e.log.V(1).Info("specs are not equal, needs updating", "currentDNSEdnpoint", currentDNSEndpoint, "newDNSEndpoint", newDNSEndpoint)
		currentDNSEndpoint.Spec = newDNSEndpoint.Spec
		currentDNSEndpoint.Labels = newDNSEndpoint.Labels
		currentDNSEndpoint.Annotations = newDNSEndpoint.Annotations
		err = e.client.Update(context, currentDNSEndpoint, &client.UpdateOptions{})
		if err != nil {
			e.log.Error(err, "unable to update", "DNSEndpoint", newDNSEndpoint)
			return err
		}
	}

	return nil
}

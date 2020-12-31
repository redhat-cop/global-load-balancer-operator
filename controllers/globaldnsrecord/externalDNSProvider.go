package globaldnsrecord

import (
	"context"
	"reflect"

	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/scylladb/go-set/strset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/external-dns/endpoint"
)

func (r *GlobalDNSRecordReconciler) createExternalDNSRecord(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (reconcile.Result, error) {
	IPs := []string{}
	for _, endpointStatus := range endpointMap {
		if endpointStatus.err != nil {
			continue
		}
		recordIPs, err := endpointStatus.getIPs()
		if err != nil {
			r.Log.Error(err, "unale to get IPs for", "endpoint", endpointStatus)
			return r.ManageError(context, instance, endpointMap, err)
		}
		r.Log.V(1).Info("found", "IPs", recordIPs)
		IPs = append(IPs, recordIPs...)
	}
	//shake out the duplicates, althought there shouldn't be any
	IPs = strset.New(IPs...).List()
	r.Log.V(1).Info("endpoint", "ips", IPs)
	newDNSEndpoint := &endpoint.DNSEndpoint{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "externaldns.k8s.io/v1alpha1",
			Kind:       "DNSEndpoint",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        instance.Name,
			Namespace:   instance.Namespace,
			Labels:      instance.Labels,
			Annotations: globalzone.Spec.Provider.ExternalDNS.Annotations,
		},
		Spec: endpoint.DNSEndpointSpec{
			Endpoints: []*endpoint.Endpoint{
				{
					DNSName:    instance.Spec.Name,
					RecordTTL:  endpoint.TTL(instance.Spec.TTL),
					RecordType: endpoint.RecordTypeA,
					Targets:    IPs,
				},
			},
		},
	}
	currentDNSEndpoint := &endpoint.DNSEndpoint{}
	err := r.GetClient().Get(context, types.NamespacedName{
		Name:      newDNSEndpoint.Name,
		Namespace: newDNSEndpoint.Namespace,
	}, currentDNSEndpoint)

	if err != nil {
		if errors.IsNotFound(err) {
			// we need to create
			controllerutil.SetControllerReference(instance, newDNSEndpoint, r.GetScheme())
			err := r.GetClient().Create(context, newDNSEndpoint, &client.CreateOptions{})
			if err != nil {
				r.Log.Error(err, "unable to create", "DNSEndpoint", newDNSEndpoint)
				return r.ManageError(context, instance, endpointMap, err)
			}
			return r.ManageSuccess(context, instance, endpointMap)
		}
		r.Log.Error(err, "unable to lookup", "DNSEndpoint", types.NamespacedName{
			Name:      newDNSEndpoint.Name,
			Namespace: newDNSEndpoint.Namespace,
		})
		return r.ManageError(context, instance, endpointMap, err)
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
		r.Log.V(1).Info("specs are not equal, needs updating", "currentDNSEdnpoint", currentDNSEndpoint, "newDNSEndpoint", newDNSEndpoint)
		currentDNSEndpoint.Spec = newDNSEndpoint.Spec
		currentDNSEndpoint.Labels = newDNSEndpoint.Labels
		currentDNSEndpoint.Annotations = newDNSEndpoint.Annotations
		err = r.GetClient().Update(context, currentDNSEndpoint, &client.UpdateOptions{})
		if err != nil {
			r.Log.Error(err, "unable to update", "DNSEndpoint", newDNSEndpoint)
			return r.ManageError(context, instance, endpointMap, err)
		}
	}

	return r.ManageSuccess(context, instance, endpointMap)

}

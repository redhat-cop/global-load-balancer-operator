package globaldnsrecord

import (
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/external-dns/endpoint"
)

func (r *ReconcileGlobalDNSRecord) createExternalDNSRecord(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (reconcile.Result, error) {
	IPs := []string{}
	for _, endpointStatus := range endpointMap {
		recordIPs, err := endpointStatus.getIPs()
		if err != nil {
			log.Error(err, "unale to get IPs for", "endpoint", endpointStatus)
			return r.ManageError(instance, err)
		}
		IPs = append(IPs, recordIPs...)
	}
	log.V(1).Info("endpoint", "ips", IPs)
	endpoint := &endpoint.DNSEndpoint{
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
			[]*endpoint.Endpoint{
				{
					DNSName:    instance.Spec.Name,
					RecordTTL:  endpoint.TTL(instance.Spec.TTL),
					RecordType: endpoint.RecordTypeA,
					Targets:    IPs,
				},
			},
		},
	}
	err := r.CreateOrUpdateResource(instance, "", endpoint)
	if err != nil {
		log.Error(err, "unable to create or update", "DNSEndpoint", endpoint)
		return r.ManageError(instance, err)
	}

	return r.ManageSuccess(instance)

}

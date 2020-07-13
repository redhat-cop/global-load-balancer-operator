package globaldnsrecord

import (
	"errors"

	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *ReconcileGlobalDNSRecord) createRoute53Record(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (reconcile.Result, error) {
	//TODO
	err := errors.New("not implemented")
	return r.ManageError(instance, err)
}

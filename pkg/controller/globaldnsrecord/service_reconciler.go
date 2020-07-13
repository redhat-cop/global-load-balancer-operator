package globaldnsrecord

import (
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ reconcile.Reconciler = &ServiceReconciler{}

type ServiceReconciler struct {
	util.ReconcilerBase
	statusChange chan<- event.GenericEvent
	parent       *redhatcopv1alpha1.GlobalDNSRecord
}

func newServiceReconciler(mgr manager.Manager, statusChange chan<- event.GenericEvent, endpoint redhatcopv1alpha1.Endpoint, parent *redhatcopv1alpha1.GlobalDNSRecord) (reconcile.Reconciler, error) {
	controllerName := getEndpointKey(endpoint)

	serviceReconciler := &ServiceReconciler{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),
		statusChange:   statusChange,
		parent:         parent,
	}

	controller, err := controller.New("controllerName", mgr, controller.Options{Reconciler: serviceReconciler})
	if err != nil {
		log.Error(err, "unable to create new controller", "with reconciler", serviceReconciler)
		return nil, err
	}

	err = controller.Watch(&source.Kind{Type: &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
	}}, &handler.EnqueueRequestForObject{}, &matchService{NamespacedName: endpoint.LoadBalancerServiceRef})
	if err != nil {
		log.Error(err, "unable to create new watch", "for service", endpoint.LoadBalancerServiceRef)
		return nil, err
	}

	return serviceReconciler, nil

}

type matchService struct {
	redhatcopv1alpha1.NamespacedName
	predicate.Funcs
}

// Update implements default UpdateEvent filter for validating resource version change
func (p *matchService) Update(e event.UpdateEvent) bool {
	if e.MetaNew.GetNamespace() == p.Namespace && e.MetaNew.GetName() == p.Name {
		return true
	}
	return false
}

func (p *matchService) Create(e event.CreateEvent) bool {
	if e.Meta.GetNamespace() == p.Namespace && e.Meta.GetName() == p.Name {
		return true
	}
	return false
}

func (p *matchService) Delete(e event.DeleteEvent) bool {
	if e.Meta.GetNamespace() == p.Namespace && e.Meta.GetName() == p.Name {
		return true
	}
	return false
}

func (sr *ServiceReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	sr.statusChange <- event.GenericEvent{
		Meta:   &sr.parent.ObjectMeta,
		Object: sr.parent,
	}
	return reconcile.Result{}, nil
}

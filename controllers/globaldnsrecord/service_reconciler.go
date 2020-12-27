package globaldnsrecord

import (
	"context"

	"github.com/go-logr/logr"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/controllers/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ reconcile.Reconciler = &ServiceReconciler{}

type ServiceReconciler struct {
	log logr.Logger
	util.ReconcilerBase
	statusChange  chan<- event.GenericEvent
	remoteManager *remotemanager.RemoteManager
	parentClient  client.Client
}

func newServiceReconciler(mgr *remotemanager.RemoteManager, statusChange chan<- event.GenericEvent, endpoint redhatcopv1alpha1.Endpoint, parentClient client.Client) (reconcile.Reconciler, error) {
	controllerName := endpoint.GetKey()

	serviceReconciler := &ServiceReconciler{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),
		statusChange:   statusChange,
		remoteManager:  mgr,
		parentClient:   parentClient,
		log:            ctrl.Log.WithName("controllers").WithName("ServiceReconciler").WithName(endpoint.GetKey()),
	}

	controller, err := controller.New(endpoint.GetKey(), mgr.Manager, controller.Options{Reconciler: serviceReconciler})
	if err != nil {
		serviceReconciler.log.Error(err, "unable to create new controller", "with reconciler", serviceReconciler)
		return nil, err
	}

	err = controller.Watch(&source.Kind{Type: &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
	}}, &handler.EnqueueRequestForObject{}, &matchService{NamespacedName: endpoint.LoadBalancerServiceRef})
	if err != nil {
		serviceReconciler.log.Error(err, "unable to create new watch", "for service", endpoint.LoadBalancerServiceRef)
		return nil, err
	}

	return serviceReconciler, nil

}

func (r *ServiceReconciler) Reconcile(context context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("globaldnsrecord", request.NamespacedName)

	// Fetch the GlobalDNSRecord instance
	instance := &corev1.Service{}
	err := r.GetClient().Get(context, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	globalDNSRecords := &redhatcopv1alpha1.GlobalDNSRecordList{}
	err = r.parentClient.List(context, globalDNSRecords, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all globalDNSRecords")
		return r.manageStatus(err)
	}
	for _, gloglobalDNSRecord := range globalDNSRecords.Items {
		for _, endpoint := range gloglobalDNSRecord.Spec.Endpoints {
			if endpoint.GetKey() == r.remoteManager.GetKey() {
				serviceRef := endpoint.CredentialsSecretRef
				if serviceRef.Namespace == instance.Namespace && serviceRef.Name == instance.Name {
					r.statusChange <- event.GenericEvent{
						Object: &gloglobalDNSRecord,
					}
				}
			}
		}
	}

	return r.manageStatus(nil)
}

func (r *ServiceReconciler) manageStatus(err error) (reconcile.Result, error) {
	var condition metav1.Condition
	if err == nil {
		condition = metav1.Condition{
			Type:               apis.ReconcileSuccess,
			LastTransitionTime: metav1.Now(),
			Reason:             apis.ReconcileSuccessReason,
			Status:             metav1.ConditionTrue,
		}
	} else {
		condition = metav1.Condition{
			Type:               apis.ReconcileError,
			LastTransitionTime: metav1.Now(),
			Message:            err.Error(),
			Reason:             apis.ReconcileErrorReason,
			Status:             metav1.ConditionTrue,
		}
	}
	r.remoteManager.SetStatus([]metav1.Condition{condition})
	return reconcile.Result{}, err
}

type matchService struct {
	redhatcopv1alpha1.NamespacedName
	predicate.Funcs
}

// Update implements default UpdateEvent filter for validating resource version change
func (p *matchService) Update(e event.UpdateEvent) bool {
	if e.ObjectNew.GetNamespace() == p.Namespace && e.ObjectNew.GetName() == p.Name {
		return true
	}
	return false
}

func (p *matchService) Create(e event.CreateEvent) bool {
	if e.Object.GetNamespace() == p.Namespace && e.Object.GetName() == p.Name {
		return true
	}
	return false
}

func (p *matchService) Delete(e event.DeleteEvent) bool {
	if e.Object.GetNamespace() == p.Namespace && e.Object.GetName() == p.Name {
		return true
	}
	return false
}

package globaldnsrecord

import (
	"context"

	astatus "github.com/operator-framework/operator-sdk/pkg/ansible/controller/status"
	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	}

	controller, err := controller.New(endpoint.GetKey(), mgr.Manager, controller.Options{Reconciler: serviceReconciler})
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

func (r *ServiceReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Service")

	// Fetch the GlobalDNSRecord instance
	instance := &corev1.Service{}
	err := r.GetClient().Get(context.TODO(), request.NamespacedName, instance)
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
	err = r.parentClient.List(context.TODO(), globalDNSRecords, &client.ListOptions{})
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
						Meta:   &gloglobalDNSRecord.ObjectMeta,
						Object: &gloglobalDNSRecord,
					}
				}
			}
		}
	}

	return r.manageStatus(nil)
}

func (r *ServiceReconciler) manageStatus(err error) (reconcile.Result, error) {
	var condition status.Condition
	if err == nil {
		condition = status.Condition{
			Type:               "ReconcileSuccess",
			LastTransitionTime: metav1.Now(),
			Message:            astatus.SuccessfulMessage,
			Reason:             astatus.SuccessfulReason,
			Status:             corev1.ConditionTrue,
		}
	} else {
		condition = status.Condition{
			Type:               "ReconcileError",
			LastTransitionTime: metav1.Now(),
			Message:            err.Error(),
			Reason:             astatus.FailedReason,
			Status:             corev1.ConditionTrue,
		}
	}
	r.remoteManager.SetStatus(status.NewConditions(condition))
	return reconcile.Result{}, err
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

package globalroutediscovery

import (
	"context"
	"errors"
	"strings"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	astatus "github.com/operator-framework/operator-sdk/pkg/ansible/controller/status"
	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ reconcile.Reconciler = &RouteReconciler{}

type RouteReconciler struct {
	parentClient client.Client
	util.ReconcilerBase
	statusChange  chan<- event.GenericEvent
	remoteManager *remotemanager.RemoteManager
}

func (r *ReconcileGlobalRouteDiscovery) newRouteReconciler(mgr *remotemanager.RemoteManager, reconcileEventChannel chan event.GenericEvent, cluster redhatcopv1alpha1.ClusterReference, parentClient client.Client) (*RouteReconciler, error) {

	controllerName := cluster.ClusterName

	routeReconciler := &RouteReconciler{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),
		statusChange:   reconcileEventChannel,
		remoteManager:  mgr,
		parentClient:   parentClient,
	}

	controller, err := controller.New(controllerName, mgr.Manager, controller.Options{Reconciler: routeReconciler})
	if err != nil {
		log.Error(err, "unable to create new controller", "with reconciler", routeReconciler)
		return nil, err
	}

	filterIngressControllerAndLoadBalancerService := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			service, ok := e.ObjectNew.(*corev1.Service)
			if !ok {
				err := errors.New("event not for a corev1.service")
				log.Error(err, "unable to convert event object to service")
				return false
			}
			return service.Spec.Type == corev1.ServiceTypeLoadBalancer && service.Namespace == "openshift-ingress"

		},
		CreateFunc: func(e event.CreateEvent) bool {
			service, ok := e.Object.(*corev1.Service)
			if !ok {
				err := errors.New("event not for a corev1.service")
				log.Error(err, "unable to convert event object to service")
				return false
			}
			return service.Spec.Type == corev1.ServiceTypeLoadBalancer && service.Namespace == "openshift-ingress"
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			service, ok := e.Object.(*corev1.Service)
			if !ok {
				err := errors.New("event not for a corev1.service")
				log.Error(err, "unable to convert event object to service")
				return false
			}
			return service.Spec.Type == corev1.ServiceTypeLoadBalancer && service.Namespace == "openshift-ingress"
		},

		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	err = controller.Watch(&source.Kind{Type: &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
	}}, &EnqueueRequestForRoutesMatchingService{RouteReconciler: routeReconciler}, filterIngressControllerAndLoadBalancerService)
	if err != nil {
		log.Error(err, "unable to create new watch for service")
		return nil, err
	}

	err = controller.Watch(&source.Kind{Type: &operatorv1.IngressController{
		TypeMeta: metav1.TypeMeta{
			Kind: "IngressController",
		},
	}}, &enqueueRequestForRoutesMatchingIngressController{RouteReconciler: routeReconciler})
	if err != nil {
		log.Error(err, "unable to create new watch for ingresscontroller")
		return nil, err
	}

	err = controller.Watch(&source.Kind{Type: &routev1.Route{
		TypeMeta: metav1.TypeMeta{
			Kind: "Route",
		},
	}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		log.Error(err, "unable to create new watch for ingresscontroller")
		return nil, err
	}

	return routeReconciler, nil

}

func (r *RouteReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling GlobalRouteDiscovery")

	// Fetch the GlobalRouteDiscovery instance
	instance := &routev1.Route{}
	err := r.GetClient().Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return r.manageStatus(err)
	}
	//find all GlobalDNSRoutes for which this route apply
	globalRouteDiscoveries := &redhatcopv1alpha1.GlobalRouteDiscoveryList{}
	err = r.parentClient.List(context.TODO(), globalRouteDiscoveries, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all globalRouteDiscoveries")
		return r.manageStatus(err)
	}
	for _, globglobalRouteDiscovery := range globalRouteDiscoveries.Items {
		labelSelector, err := metav1.LabelSelectorAsSelector(&globglobalRouteDiscovery.Spec.RouteSelector)
		if err != nil {
			log.Error(err, "unable to convert label selector to selector", "label selector", globglobalRouteDiscovery.Spec.RouteSelector)
			return r.manageStatus(err)
		}
		if labelSelector.Matches(labels.Set(instance.Labels)) {
			r.statusChange <- event.GenericEvent{
				Meta:   &globglobalRouteDiscovery.ObjectMeta,
				Object: &globglobalRouteDiscovery,
			}
		}
	}
	return r.manageStatus(nil)
}

func (r *RouteReconciler) manageStatus(err error) (reconcile.Result, error) {
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

type enqueueRequestForRoutesMatchingIngressController struct {
	*RouteReconciler
}

func (r *RouteReconciler) findAllRoutes() ([]routev1.Route, error) {
	routeList := &routev1.RouteList{}
	err := r.GetClient().List(context.TODO(), routeList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all routes")
		return []routev1.Route{}, err
	}
	return routeList.Items, nil
}

func (r *RouteReconciler) findAndEnqueueRoutesForIngressController(name string, q workqueue.RateLimitingInterface) {
	routes, err := r.findAllRoutes()
	if err != nil {
		log.Error(err, "unable to find all routes")
		return
	}
	for _, route := range routes {
		for _, ingress := range route.Status.Ingress {
			if ingress.RouterName == name {
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      route.Name,
					Namespace: route.Namespace,
				}})
			}
		}
	}
}

func (e *enqueueRequestForRoutesMatchingIngressController) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	e.findAndEnqueueRoutesForIngressController(evt.Meta.GetName(), q)
}

func (e *enqueueRequestForRoutesMatchingIngressController) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	e.findAndEnqueueRoutesForIngressController(evt.MetaNew.GetName(), q)
}

// Delete implements EventHandler
func (e *enqueueRequestForRoutesMatchingIngressController) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	e.findAndEnqueueRoutesForIngressController(evt.Meta.GetName(), q)
}

// Generic implements EventHandler
func (e *enqueueRequestForRoutesMatchingIngressController) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

type EnqueueRequestForRoutesMatchingService struct {
	*RouteReconciler
}

func (r *RouteReconciler) lookupIngressControllerForService(service *corev1.Service) (*operatorv1.IngressController, bool, error) {
	ingressController := &operatorv1.IngressController{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Namespace: "openshift-ingress-operator",
		Name:      strings.TrimPrefix(service.Name, "router-"),
	}, ingressController)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &operatorv1.IngressController{}, false, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "unable to look up", "ingress controller", strings.TrimPrefix(service.Name, "router-"))
		return &operatorv1.IngressController{}, false, err
	}
	return ingressController, true, nil
}

func (r *RouteReconciler) findAndEnqueueRoutesForService(service *corev1.Service, q workqueue.RateLimitingInterface) {
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return
	}
	if service.GetNamespace() != "openshift-ingress" {
		return
	}
	ingressController, found, err := r.lookupIngressControllerForService(service)
	if err != nil {
		log.Error(err, "unable to lookup ingress controller", "for service", service)
		return
	}
	if !found {
		return
	}
	r.findAndEnqueueRoutesForIngressController(ingressController.Name, q)
}

func (e *EnqueueRequestForRoutesMatchingService) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	service, ok := evt.Object.(*corev1.Service)
	if !ok {
		err := errors.New("event not for a corev1.service")
		log.Error(err, "unable to convert event object to service")
		return
	}
	e.findAndEnqueueRoutesForService(service, q)
}

func (e *EnqueueRequestForRoutesMatchingService) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	service, ok := evt.ObjectNew.(*corev1.Service)
	if !ok {
		err := errors.New("event not for a corev1.service")
		log.Error(err, "unable to convert event object to service")
		return
	}
	e.findAndEnqueueRoutesForService(service, q)
}

// Delete implements EventHandler
func (e *EnqueueRequestForRoutesMatchingService) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	service, ok := evt.Object.(*corev1.Service)
	if !ok {
		err := errors.New("event not for a corev1.service")
		log.Error(err, "unable to convert event object to service")
		return
	}
	e.findAndEnqueueRoutesForService(service, q)
}

// Generic implements EventHandler
func (e *EnqueueRequestForRoutesMatchingService) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

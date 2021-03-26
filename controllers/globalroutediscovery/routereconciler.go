package globalroutediscovery

import (
	"context"
	"errors"
	"strings"

	"github.com/go-logr/logr"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/controllers/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
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
	Log          logr.Logger
	util.ReconcilerBase
	statusChange  chan<- event.GenericEvent
	remoteManager *remotemanager.RemoteManager
}

func (r *GlobalRouteDiscoveryReconciler) newRouteReconciler(mgr *remotemanager.RemoteManager, reconcileEventChannel chan<- event.GenericEvent, cluster redhatcopv1alpha1.ClusterReference, parentClient client.Client) (*RouteReconciler, error) {

	controllerName := cluster.ClusterName

	routeReconciler := &RouteReconciler{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName), mgr.GetAPIReader()),
		statusChange:   reconcileEventChannel,
		remoteManager:  mgr,
		parentClient:   parentClient,
		Log:            ctrl.Log.WithName("controllers").WithName("Route"),
	}

	controller, err := controller.New(controllerName, mgr.Manager, controller.Options{Reconciler: routeReconciler})
	if err != nil {
		r.Log.Error(err, "unable to create new controller", "with reconciler", routeReconciler)
		return nil, err
	}

	filterIngressControllerAndLoadBalancerService := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			service, ok := e.ObjectNew.(*corev1.Service)
			if !ok {
				err := errors.New("event not for a corev1.service")
				r.Log.Error(err, "unable to convert event object to service")
				return false
			}
			return service.Spec.Type == corev1.ServiceTypeLoadBalancer && service.Namespace == "openshift-ingress"

		},
		CreateFunc: func(e event.CreateEvent) bool {
			service, ok := e.Object.(*corev1.Service)
			if !ok {
				err := errors.New("event not for a corev1.service")
				r.Log.Error(err, "unable to convert event object to service")
				return false
			}
			return service.Spec.Type == corev1.ServiceTypeLoadBalancer && service.Namespace == "openshift-ingress"
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			service, ok := e.Object.(*corev1.Service)
			if !ok {
				err := errors.New("event not for a corev1.service")
				r.Log.Error(err, "unable to convert event object to service")
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
	}}, &EnqueueRequestForRoutesMatchingService{
		RouteReconciler: routeReconciler,
		Log:             ctrl.Log.WithName("handler").WithName("EnqueueRequestForRoutesMatchingService"),
	}, filterIngressControllerAndLoadBalancerService)
	if err != nil {
		r.Log.Error(err, "unable to create new watch for service")
		return nil, err
	}

	err = controller.Watch(&source.Kind{Type: &operatorv1.IngressController{
		TypeMeta: metav1.TypeMeta{
			Kind: "IngressController",
		},
	}}, &enqueueRequestForRoutesMatchingIngressController{RouteReconciler: routeReconciler})
	if err != nil {
		r.Log.Error(err, "unable to create new watch for ingresscontroller")
		return nil, err
	}

	err = controller.Watch(&source.Kind{Type: &routev1.Route{
		TypeMeta: metav1.TypeMeta{
			Kind: "Route",
		},
	}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		r.Log.Error(err, "unable to create new watch for ingresscontroller")
		return nil, err
	}

	return routeReconciler, nil

}

func (r *RouteReconciler) Reconcile(context context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.Log.WithValues("route", request.NamespacedName)

	// Fetch the GlobalRouteDiscovery instance
	instance := &routev1.Route{}
	err := r.GetClient().Get(context, request.NamespacedName, instance)
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
	err = r.parentClient.List(context, globalRouteDiscoveries, &client.ListOptions{})
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
			log.V(1).Info("route match found", "route", instance, "global route discovery", globglobalRouteDiscovery)
			r.statusChange <- event.GenericEvent{
				Object: &globglobalRouteDiscovery,
			}
		}
	}
	return r.manageStatus(nil)
}

func (r *RouteReconciler) manageStatus(err error) (reconcile.Result, error) {
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
	r.remoteManager.SetStatus(apis.AddOrReplaceCondition(condition, r.remoteManager.GetStatus()))
	return reconcile.Result{}, err
}

type enqueueRequestForRoutesMatchingIngressController struct {
	*RouteReconciler
}

func (r *RouteReconciler) findAllRoutes() ([]routev1.Route, error) {
	routeList := &routev1.RouteList{}
	err := r.GetClient().List(context.TODO(), routeList, &client.ListOptions{})
	if err != nil {
		r.Log.Error(err, "unable to list all routes")
		return []routev1.Route{}, err
	}
	return routeList.Items, nil
}

func (r *RouteReconciler) findAndEnqueueRoutesForIngressController(name string, q workqueue.RateLimitingInterface) {
	routes, err := r.findAllRoutes()
	if err != nil {
		r.Log.Error(err, "unable to find all routes")
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
	e.findAndEnqueueRoutesForIngressController(evt.Object.GetName(), q)
}

func (e *enqueueRequestForRoutesMatchingIngressController) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	e.findAndEnqueueRoutesForIngressController(evt.ObjectNew.GetName(), q)
}

// Delete implements EventHandler
func (e *enqueueRequestForRoutesMatchingIngressController) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	e.findAndEnqueueRoutesForIngressController(evt.Object.GetName(), q)
}

// Generic implements EventHandler
func (e *enqueueRequestForRoutesMatchingIngressController) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

type EnqueueRequestForRoutesMatchingService struct {
	*RouteReconciler
	Log logr.Logger
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
		r.Log.Error(err, "unable to look up", "ingress controller", strings.TrimPrefix(service.Name, "router-"))
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
		r.Log.Error(err, "unable to lookup ingress controller", "for service", service)
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
		e.Log.Error(err, "unable to convert event object to service")
		return
	}
	e.findAndEnqueueRoutesForService(service, q)
}

func (e *EnqueueRequestForRoutesMatchingService) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	service, ok := evt.ObjectNew.(*corev1.Service)
	if !ok {
		err := errors.New("event not for a corev1.service")
		e.Log.Error(err, "unable to convert event object to service")
		return
	}
	e.findAndEnqueueRoutesForService(service, q)
}

// Delete implements EventHandler
func (e *EnqueueRequestForRoutesMatchingService) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	service, ok := evt.Object.(*corev1.Service)
	if !ok {
		err := errors.New("event not for a corev1.service")
		e.Log.Error(err, "unable to convert event object to service")
		return
	}
	e.findAndEnqueueRoutesForService(service, q)
}

// Generic implements EventHandler
func (e *EnqueueRequestForRoutesMatchingService) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

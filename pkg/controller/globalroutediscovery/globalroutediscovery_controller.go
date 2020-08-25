package globalroutediscovery

import (
	"context"
	errs "errors"
	"fmt"
	"reflect"

	"strconv"

	routev1 "github.com/openshift/api/route/v1"
	astatus "github.com/operator-framework/operator-sdk/pkg/ansible/controller/status"
	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "globalroutediscovery-controller"
const loadBalancingPolicyAnnotation = "global-load-balancer-operator.redhat-cop.io/load-balancing-policy"
const containerProbeAnnotation = "global-load-balancer-operator.redhat-cop.io/container-probe"
const tTLAnnotation = "global-load-balancer-operator.redhat-cop.io/ttl"

var log = logf.Log.WithName(controllerName)

// ReconcileGlobalRouteDiscovery reconciles a GlobalRouteDiscovery object
type ReconcileGlobalRouteDiscovery struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
	apireader client.Reader
}

//var remoteManagersMap = sync.Map{}
var remoteManagersMap = map[string]*remotemanager.RemoteManager{}
var reconcileEventChannel chan event.GenericEvent = make(chan event.GenericEvent)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new GlobalRouteDiscovery Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileGlobalRouteDiscovery{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),
		apireader:      mgr.GetAPIReader(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("globalroutediscovery-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource GlobalRouteDiscovery
	err = c.Watch(&source.Kind{Type: &redhatcopv1alpha1.GlobalRouteDiscovery{
		TypeMeta: metav1.TypeMeta{
			Kind: "GlobalRouteDiscovery",
		},
	}}, &handler.EnqueueRequestForObject{}, util.ResourceGenerationOrFinalizerChangedPredicate{})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource GlobalRouteDiscovery
	err = c.Watch(&source.Kind{Type: &redhatcopv1alpha1.GlobalDNSRecord{
		TypeMeta: metav1.TypeMeta{
			Kind: "GlobalDNSRecord",
		},
	}}, &handler.EnqueueRequestForOwner{
		OwnerType:    &redhatcopv1alpha1.GlobalRouteDiscovery{},
		IsController: true,
	}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	//create watch to receive events
	err = c.Watch(
		&source.Channel{Source: reconcileEventChannel},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileGlobalRouteDiscovery implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileGlobalRouteDiscovery{}

// Reconcile reads that state of the cluster for a GlobalRouteDiscovery object and makes changes based on the state read
// and what is in the GlobalRouteDiscovery.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileGlobalRouteDiscovery) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling GlobalRouteDiscovery")

	// Fetch the GlobalRouteDiscovery instance
	instance := &redhatcopv1alpha1.GlobalRouteDiscovery{}
	err := r.apireader.Get(context.TODO(), request.NamespacedName, instance)
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

	if ok := r.IsInitialized(instance); !ok {
		err := r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(instance, err)
		}
		return reconcile.Result{}, nil
	}

	if util.IsBeingDeleted(instance) {
		if !util.HasFinalizer(instance, controllerName) {
			return reconcile.Result{}, nil
		}
		err = r.ensureRemoteManagers()
		if err != nil {
			log.Error(err, "unable to delete instance", "instance", instance.GetName())
			return r.ManageError(instance, err)
		}
		util.RemoveFinalizer(instance, controllerName)
		err = r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(instance, err)
		}
		return reconcile.Result{}, nil
	}

	//ensure remote managers exist for all of the isntances and stop those that should not exist anymore.
	err = r.ensureRemoteManagers()
	if err != nil {
		log.Error(err, "unable to ensure remote manager are correctly configured")
		return r.ManageError(instance, err)
	}

	log.V(1).Info("after manager creation")

	clusterClientMap := map[redhatcopv1alpha1.ClusterReference]client.Client{}

	for _, clusterReference := range instance.Spec.Clusters {
		restConfig, err := r.getRestConfig(clusterReference)
		if err != nil {
			log.Error(err, "unable to create rest config", "for cluster", clusterReference)
			return r.ManageError(instance, err)
		}
		client, err := client.New(restConfig, client.Options{
			Scheme: r.GetScheme(),
		})
		if err != nil {
			log.Error(err, "unable to create client", "for cluster", clusterReference)
			return r.ManageError(instance, err)
		}

		clusterClientMap[clusterReference] = client
	}

	qualifyingRoutes := []RouteInfo{}

	//for each cluster, select qualifying routes
	for _, clusterReference := range instance.Spec.Clusters {
		routes, err := findQualifyingRoutes(instance, clusterClientMap[clusterReference])
		if err != nil {
			log.Error(err, "unable to list qualifying routes", "for cluster", clusterReference)
			return r.ManageError(instance, err)
		}
		routeInfos := []RouteInfo{}
		for _, route := range routes {
			routeInfo := RouteInfo{
				Route:            route,
				ClusterReference: clusterReference,
			}
			probe, found, err := findProbeForRoute(route, clusterClientMap[clusterReference])
			if err != nil {
				log.Error(err, "error finding probe", "for route", route, "for cluster", clusterReference)
				return r.ManageError(instance, err)
			}
			if found {
				routeInfo.ReadinessCheck = probe
			}
			service, err := findIngressControllerServiceForRoute(route, clusterClientMap[clusterReference])
			if err != nil {
				log.Error(err, "error finding service", "for route", route, "for cluster", clusterReference)
				return r.ManageError(instance, err)
			}
			routeInfo.Service = *service
			loadBalancingPolicy, ok := route.Annotations[loadBalancingPolicyAnnotation]
			if ok {
				routeInfo.LoadBalancigPolicy = redhatcopv1alpha1.LoadBalancingPolicy(loadBalancingPolicy)
			} else {
				routeInfo.LoadBalancigPolicy = instance.Spec.DefaultLoadBalancingPolicy
			}
			ttl, ok := route.Annotations[tTLAnnotation]
			if ok {
				ttlint, err := strconv.Atoi(ttl)
				if err != nil {
					log.Error(err, "unable to convert ttl to int", "ttl", ttl, "for route", route, "for cluster", clusterReference)
					return r.ManageError(instance, err)
				}
				routeInfo.TTL = ttlint
			} else {
				routeInfo.TTL = instance.Spec.DefaultTTL
			}
			routeInfos = append(routeInfos, routeInfo)
		}
		qualifyingRoutes = append(qualifyingRoutes, routeInfos...)
		//for each route determine serving ingress controller and corresponding service
		//for each route determine service and pod, if pod has a http readiness helthcheck track it
		//if route has an annotation for lb policy track it.
		//map[server]routeinfo
	}
	log.V(1).Info("found", "qualifying routes", len(qualifyingRoutes))
	//reorder data structure per route
	//map[route][]routeinfo+endpointinfo
	routeRouteInfoSMap := map[string][]RouteInfo{}

	for _, routeInfo := range qualifyingRoutes {
		routeRouteInfoSMap[apis.GetKeyShort(&routeInfo.Route)] = append(routeRouteInfoSMap[apis.GetKeyShort(&routeInfo.Route)], routeInfo)
	}
	log.V(1).Info("rounte", "infos map len", len(routeRouteInfoSMap))
	//create or update GlobalDNSRecords
	for name, routeInfos := range routeRouteInfoSMap {
		globaDNSRecord, err := getGlobalDNSRecord(instance, name, routeInfos)
		if err != nil {
			log.Error(err, "unsable to create global dns record", "for route infos", routeInfos)
			return r.ManageError(instance, err)
		}
		err = r.CreateOrUpdateResource(instance, instance.Namespace, globaDNSRecord)
		if err != nil {
			log.Error(err, "unsable to create or update", "global dns record", globaDNSRecord)
			return r.ManageError(instance, err)
		}
	}

	//log.V(1).Info("about to update status")
	return r.ManageSuccess(instance)
}

func getGlobalDNSRecord(instance *redhatcopv1alpha1.GlobalRouteDiscovery, name string, routeInfos []RouteInfo) (*redhatcopv1alpha1.GlobalDNSRecord, error) {
	if len(routeInfos) == 0 {
		err := errs.New("no route info")
		log.Error(err, "at least one route info must be passed", "routeInfos", routeInfos)
		return nil, err
	}
	log.V(1).Info("route info array", "len", len(routeInfos))
	route0 := routeInfos[0]
	// consistency checks
	for i, route := range routeInfos {
		routeNumberString := fmt.Sprintf("route[%d]", i)
		if route.Route.Spec.Host != route0.Route.Spec.Host {
			err := errs.New("route's hosts must match")
			log.Error(err, "route's host not matching", "route[0]", route0.Route.Spec.Host, routeNumberString, route.Route.Spec.Host)
			return nil, err
		}
		if route.LoadBalancigPolicy != route0.LoadBalancigPolicy {
			err := errs.New("route's load balancing policy must match")
			log.Error(err, "route's load balancing policy not matching", "route[0]", route0.LoadBalancigPolicy, routeNumberString, route.LoadBalancigPolicy)
			return nil, err
		}
		if route.TTL != route0.TTL {
			err := errs.New("route's TTLs must match")
			log.Error(err, "route's TTLs not matching", "route[0]", route0.TTL, routeNumberString, route.TTL)
			return nil, err
		}
		if !reflect.DeepEqual(route.ReadinessCheck, route0.ReadinessCheck) {
			err := errs.New("route's health checks must match")
			log.Error(err, "route's health checks not matching", "route[0]", route0.ReadinessCheck, routeNumberString, route.ReadinessCheck)
			return nil, err
		}
	}
	globaldnsrecord := &redhatcopv1alpha1.GlobalDNSRecord{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "redhatcop.redhat.io/v1alpha1",
			Kind:       "GlobalDNSRecord",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.Namespace,
			Name:      route0.Route.Spec.Host,
		},
		Spec: redhatcopv1alpha1.GlobalDNSRecordSpec{
			Name:                route0.Route.Spec.Host,
			GlobalZoneRef:       instance.Spec.GlobalZoneRef,
			LoadBalancingPolicy: route0.LoadBalancigPolicy,
			TTL:                 route0.TTL,
		},
	}
	endpoints := []redhatcopv1alpha1.Endpoint{}
	for _, routeInfo := range routeInfos {
		endpoint := redhatcopv1alpha1.Endpoint{
			ClusterName:          routeInfo.ClusterReference.ClusterName,
			CredentialsSecretRef: routeInfo.ClusterReference.CredentialsSecretRef,
			LoadBalancerServiceRef: redhatcopv1alpha1.NamespacedName{
				Name:      routeInfo.Service.Name,
				Namespace: routeInfo.Service.Namespace,
			},
		}
		log.V(1).Info("created", "endpoint", endpoint)
		endpoints = append(endpoints, endpoint)
	}
	globaldnsrecord.Spec.Endpoints = endpoints
	if route0.ReadinessCheck != nil && route0.ReadinessCheck.HTTPGet != nil {
		globaldnsrecord.Spec.HealthCheck = getHealthCheck(&route0.Route, route0.ReadinessCheck)
	}
	return globaldnsrecord, nil
}

func getHealthCheck(route *routev1.Route, probe *corev1.Probe) *corev1.Probe {
	httpHeaders := []corev1.HTTPHeader{}
	for _, header := range probe.HTTPGet.HTTPHeaders {
		if header.Name == "host" {
			continue
		}
		httpHeaders = append(httpHeaders, header)
	}
	var scheme corev1.URIScheme
	var port int
	if route.Spec.TLS != nil {
		scheme = corev1.URISchemeHTTPS
		port = 443
	} else {
		scheme = corev1.URISchemeHTTP
		port = 80
	}
	httpget := &corev1.HTTPGetAction{
		Host:        route.Spec.Host,
		HTTPHeaders: httpHeaders,
		Path:        probe.HTTPGet.Path,
		Scheme:      scheme,
		Port:        intstr.FromInt(port),
	}
	return &corev1.Probe{
		FailureThreshold:    probe.FailureThreshold,
		InitialDelaySeconds: probe.InitialDelaySeconds,
		SuccessThreshold:    probe.SuccessThreshold,
		PeriodSeconds:       probe.PeriodSeconds,
		TimeoutSeconds:      probe.TimeoutSeconds,
		Handler: corev1.Handler{
			HTTPGet: httpget,
		},
	}
}

func findIngressControllerServiceForRoute(route routev1.Route, c client.Client) (*corev1.Service, error) {
	if len(route.Status.Ingress) == 0 {
		err := errs.New("no status found")
		log.Error(err, "route has no satus", "route", route)
		return &corev1.Service{}, err
	}
	service := &corev1.Service{}
	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: "openshift-ingress",
		Name:      "router-" + route.Status.Ingress[0].RouterName,
	}, service)
	if err != nil {
		log.Error(err, "unable to lookup ingress service", "for route", route)
		return &corev1.Service{}, err
	}
	return service, nil
}

func findQualifyingRoutes(instance *redhatcopv1alpha1.GlobalRouteDiscovery, c client.Client) ([]routev1.Route, error) {
	routeList := &routev1.RouteList{}
	labelSelector, err := metav1.LabelSelectorAsSelector(&instance.Spec.RouteSelector)
	if err != nil {
		log.Error(err, "unable to convert label selector to selector", "label selector", instance.Spec.RouteSelector)
		return []routev1.Route{}, err
	}
	err = c.List(context.TODO(), routeList, &client.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		log.Error(err, "unable to list routes ", "with label selector", labelSelector)
		return []routev1.Route{}, err
	}
	return routeList.Items, nil
}

func findProbeForRoute(route routev1.Route, c client.Client) (*corev1.Probe, bool, error) {
	service := &corev1.Service{}
	err := c.Get(context.TODO(), types.NamespacedName{
		Name:      route.Spec.To.Name,
		Namespace: route.Namespace,
	}, service)
	if err != nil {
		log.Error(err, "unable to fund service associated to", "route", route)
		return &corev1.Probe{}, false, err
	}
	podList := &corev1.PodList{}
	labelSelector, err := metav1.LabelSelectorAsSelector(metav1.SetAsLabelSelector(service.Spec.Selector))
	if err != nil {
		log.Error(err, "unable to convert label selector to selector", "label selector", service.Spec.Selector)
		return &corev1.Probe{}, false, err
	}
	err = c.List(context.TODO(), podList, &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     service.Namespace,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &corev1.Probe{}, false, nil
		}
		log.Error(err, "unable to list pods", "for service", service)
		return &corev1.Probe{}, false, err
	}
	if len(podList.Items) == 0 {
		return &corev1.Probe{}, false, nil
	}
	pod := podList.Items[0]
	var cnt corev1.Container
	if containerName, ok := route.Annotations[containerProbeAnnotation]; ok {
		found := false
		for _, container := range pod.Spec.Containers {
			if container.Name == containerName {
				cnt = container
				found = true
			}
		}
		if !found {
			return &corev1.Probe{}, false, nil
		}
	} else {
		cnt = pod.Spec.Containers[0]
	}
	probe := cnt.ReadinessProbe
	if probe != nil && probe.HTTPGet != nil {
		return probe, true, nil
	}
	return &corev1.Probe{}, false, nil

}

type RouteInfo struct {
	Route              routev1.Route
	Service            corev1.Service
	ReadinessCheck     *corev1.Probe
	LoadBalancigPolicy redhatcopv1alpha1.LoadBalancingPolicy
	ClusterReference   redhatcopv1alpha1.ClusterReference
	TTL                int
}

func (r *ReconcileGlobalRouteDiscovery) ensureRemoteManagers() error {
	instances := &redhatcopv1alpha1.GlobalRouteDiscoveryList{}
	err := r.GetClient().List(context.TODO(), instances, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list GlobalRouteDiscovery")
		return err
	}
	neededClusterMap := map[string]redhatcopv1alpha1.ClusterReference{}
	neededClusterReferenceSet := strset.New()
	for _, instance := range instances.Items {
		if !util.IsBeingDeleted(&instance) {
			for _, cluster := range instance.Spec.Clusters {
				neededClusterReferenceSet.Add(cluster.GetKey())
				neededClusterMap[cluster.GetKey()] = cluster
			}
		}
	}

	currentClusterReferenceSet := strset.New()
	for cluster := range remoteManagersMap {
		currentClusterReferenceSet.Add(cluster)
	}

	toBeAdded := strset.Difference(neededClusterReferenceSet, currentClusterReferenceSet)
	toBeRemoved := strset.Difference(currentClusterReferenceSet, neededClusterReferenceSet)

	log.V(1).Info("to be added", "remote managers", toBeAdded)
	log.V(1).Info("to be removed", "remote managers", toBeRemoved)

	for _, cluster := range toBeRemoved.List() {
		defer delete(remoteManagersMap, cluster)
		remoteManager, ok := remoteManagersMap[cluster]
		if !ok {
			continue
		}
		remoteManager.Stop()
	}

	for _, cluster := range toBeAdded.List() {
		remoteManager, err := r.createRemoteManager(neededClusterMap[cluster])
		if err != nil {
			log.Error(err, "unable to create remote manager", "for cluster", cluster)
			return err
		}
		remoteManagersMap[cluster] = remoteManager
		remoteManager.Start()
	}
	log.V(1).Info("remote manager map", "size", len(remoteManagersMap))
	return nil
}

func (r *ReconcileGlobalRouteDiscovery) createRemoteManager(cluster redhatcopv1alpha1.ClusterReference) (*remotemanager.RemoteManager, error) {
	restConfig, err := r.getRestConfig(cluster)
	if err != nil {
		log.Error(err, "unable to create client for", "cluster", cluster)
		return nil, err
	}
	options := manager.Options{
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Scheme:             r.GetScheme(),
	}

	remoteManager, err := remotemanager.NewRemoteManager(restConfig, options, cluster.ClusterName+"@"+cluster.CredentialsSecretRef.Namespace+"/"+cluster.CredentialsSecretRef.Name)
	if err != nil {
		log.Error(err, "unable to create stoppable manager for", "cluster", cluster)
		return nil, err
	}
	_, err = r.newRouteReconciler(remoteManager, reconcileEventChannel, cluster, r.GetClient())
	if err != nil {
		log.Error(err, "unable create serviceReconciler", "for cluster", cluster)
		return nil, err
	}
	return remoteManager, nil
}

func (r *ReconcileGlobalRouteDiscovery) getRestConfig(cluster redhatcopv1alpha1.ClusterReference) (*rest.Config, error) {
	// for now we assume that we will have a secret with a kubeconfig.
	// the clustr ref is not really needed in this case.

	secret := &corev1.Secret{}

	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name:      cluster.CredentialsSecretRef.Name,
		Namespace: cluster.CredentialsSecretRef.Namespace,
	}, secret)

	if err != nil {
		log.Error(err, "unable to find", "secret", cluster.CredentialsSecretRef)
		return nil, err
	}

	kubeconfig, ok := secret.Data["kubeconfig"]

	if !ok {
		err := errs.New("unable to find kubeconfig key in secret")
		log.Error(err, "", "secret", cluster.CredentialsSecretRef)
		return nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		log.Error(err, "unable to create rest config", "kubeconfig", kubeconfig)
		return nil, err
	}

	return restConfig, nil

}

//ManageError manage error sets an error status in the CR and fires an event, finally it returns the error so the operator can re-attempt
func (r *ReconcileGlobalRouteDiscovery) ManageError(instance *redhatcopv1alpha1.GlobalRouteDiscovery, issue error) (reconcile.Result, error) {
	log.V(1).Info("manage error called")
	r.GetRecorder().Event(instance, "Warning", "ProcessingError", issue.Error())
	condition := status.Condition{
		Type:               "ReconcileError",
		LastTransitionTime: metav1.Now(),
		Message:            issue.Error(),
		Reason:             astatus.FailedReason,
		Status:             corev1.ConditionTrue,
	}
	instance.Status.Conditions = status.NewConditions(condition)
	log.V(1).Info("getting cluster referece statuses")
	instance.Status.ClusterReferenceStatuses = getClusterReferenceStatuses(instance, r.GetRecorder())
	log.V(1).Info("about to modify state for", "instance version", instance.GetResourceVersion())
	err := r.GetClient().Status().Update(context.Background(), instance)
	if err != nil {
		if errors.IsResourceExpired(err) {
			log.Info("unable to update status for", "object version", instance.GetResourceVersion(), "resource version expired, will trigger another reconcile cycle", "")
		} else {
			log.Error(err, "unable to update status for", "object", instance)
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, issue
}

func getClusterReferenceStatuses(instance *redhatcopv1alpha1.GlobalRouteDiscovery, recorder record.EventRecorder) map[string]status.Conditions {
	log.V(1).Info("entering getClusterReferenceStatuses")
	conditions := map[string]status.Conditions{}
	log.V(1).Info("remoteManagersMap", "len", len(remoteManagersMap))
	for _, clusterReference := range instance.Spec.Clusters {
		log.V(1).Info("examining", "cluster", clusterReference)
		remoteManager, ok := remoteManagersMap[clusterReference.GetKey()]
		if ok {
			log.V(1).Info("found", "remote manager", remoteManager, "status", remoteManager.GetStatus())
			conditions[remoteManager.GetKey()] = remoteManager.GetStatus()
			if conditions[remoteManager.GetKey()].GetCondition("ReconcileError") != nil && conditions[remoteManager.GetKey()].GetCondition("ReconcileError").Status == v1.ConditionTrue {
				recorder.Event(instance, "Warning", "ProcessingError", conditions[remoteManager.GetKey()].GetCondition("ReconcileError").Message)
			}
		}
	}
	log.V(1).Info("retrieved statuses", "conditions", conditions)
	return conditions
}

// ManageSuccess will update the status of the CR and return a successful reconcile result
func (r *ReconcileGlobalRouteDiscovery) ManageSuccess(instance *redhatcopv1alpha1.GlobalRouteDiscovery) (reconcile.Result, error) {
	//log.V(1).Info("manage success called")
	condition := status.Condition{
		Type:               "ReconcileSuccess",
		LastTransitionTime: metav1.Now(),
		Message:            astatus.SuccessfulMessage,
		Reason:             astatus.SuccessfulReason,
		Status:             corev1.ConditionTrue,
	}
	instance.Status.Conditions = status.NewConditions(condition)
	//log.V(1).Info("getting cluster reference statuses")
	instance.Status.ClusterReferenceStatuses = getClusterReferenceStatuses(instance, r.GetRecorder())
	//log.V(1).Info("about to modify state for", "instance version", instance.GetResourceVersion())
	//log.V(1).Info("about to update status", "instance", instance)
	err := r.GetClient().Status().Update(context.Background(), instance)
	if err != nil {
		if errors.IsResourceExpired(err) {
			log.Info("unable to update status for", "object version", instance.GetResourceVersion(), "resource version expired, will trigger another reconcile cycle", "")
		} else {
			log.Error(err, "unable to update status for", "object", instance)
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

//IsInitialized initislizes the instance, currently is simply adds a finalizer.
func (r *ReconcileGlobalRouteDiscovery) IsInitialized(obj metav1.Object) bool {
	isInitialized := true
	globalRouteDiscovery, ok := obj.(*redhatcopv1alpha1.GlobalRouteDiscovery)
	if !ok {
		log.Error(errs.New("unable to convert to egressIPAM"), "unable to convert to egressIPAM")
		return false
	}
	if !util.HasFinalizer(globalRouteDiscovery, controllerName) {
		util.AddFinalizer(globalRouteDiscovery, controllerName)
		isInitialized = false
	}
	if globalRouteDiscovery.Spec.DefaultLoadBalancingPolicy == "" {
		globalRouteDiscovery.Spec.DefaultLoadBalancingPolicy = redhatcopv1alpha1.Multivalue
		isInitialized = false
	}
	if globalRouteDiscovery.Spec.DefaultTTL == 0 {
		globalRouteDiscovery.Spec.DefaultTTL = 60
		isInitialized = false
	}
	return isInitialized
}

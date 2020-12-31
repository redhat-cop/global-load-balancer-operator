/*
Copyright 2020 Red Hat Community of Practice.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package globalroutediscovery

import (
	"context"
	"encoding/json"
	errs "errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/controllers/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "globalroutediscovery-controller"
const loadBalancingPolicyAnnotation = "global-load-balancer-operator.redhat-cop.io/load-balancing-policy"
const containerProbeAnnotation = "global-load-balancer-operator.redhat-cop.io/container-probe"
const tTLAnnotation = "global-load-balancer-operator.redhat-cop.io/ttl"
const healthCheckAnnotation = "global-load-balancer-operator.redhat-cop.io/health-check"

// GlobalRouteDiscoveryReconciler reconciles a GlobalRouteDiscovery object
type GlobalRouteDiscoveryReconciler struct {
	util.ReconcilerBase
	Log                   logr.Logger
	apireader             client.Reader
	remoteManagersMap     map[string]*remotemanager.RemoteManager
	reconcileEventChannel chan event.GenericEvent
}

// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globalroutediscoveries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globalroutediscoveries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globalroutediscoveries/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GlobalRouteDiscovery object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *GlobalRouteDiscoveryReconciler) Reconcile(context context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("globalroutediscovery", req.NamespacedName)

	// Fetch the GlobalRouteDiscovery instance
	instance := &redhatcopv1alpha1.GlobalRouteDiscovery{}
	err := r.apireader.Get(context, req.NamespacedName, instance)
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
		err := r.GetClient().Update(context, instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(context, instance, err)
		}
		return reconcile.Result{}, nil
	}

	if util.IsBeingDeleted(instance) {
		if !util.HasFinalizer(instance, controllerName) {
			return reconcile.Result{}, nil
		}
		err = r.ensureRemoteManagers(context)
		if err != nil {
			log.Error(err, "unable to delete instance", "instance", instance.GetName())
			return r.ManageError(context, instance, err)
		}
		util.RemoveFinalizer(instance, controllerName)
		err = r.GetClient().Update(context, instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(context, instance, err)
		}
		return reconcile.Result{}, nil
	}

	//ensure remote managers exist for all of the isntances and stop those that should not exist anymore.
	err = r.ensureRemoteManagers(context)
	if err != nil {
		log.Error(err, "unable to ensure remote manager are correctly configured")
		return r.ManageError(context, instance, err)
	}

	log.V(1).Info("after manager creation")

	clusterClientMap := map[redhatcopv1alpha1.ClusterReference]client.Client{}

	for _, clusterReference := range instance.Spec.Clusters {
		restConfig, err := r.getRestConfig(context, clusterReference)
		if err != nil {
			log.Error(err, "unable to create rest config", "for cluster", clusterReference)
			return r.ManageError(context, instance, err)
		}
		client, err := client.New(restConfig, client.Options{
			Scheme: r.GetScheme(),
		})
		if err != nil {
			log.Error(err, "unable to create client", "for cluster", clusterReference)
			return r.ManageError(context, instance, err)
		}

		clusterClientMap[clusterReference] = client
	}

	qualifyingRoutes := []RouteInfo{}

	//for each cluster, select qualifying routes
	for _, clusterReference := range instance.Spec.Clusters {
		routes, err := r.findQualifyingRoutes(context, instance, clusterClientMap[clusterReference])
		if err != nil {
			log.Error(err, "unable to list qualifying routes", "for cluster", clusterReference)
			return r.ManageError(context, instance, err)
		}
		routeInfos := []RouteInfo{}
		for _, route := range routes {
			routeInfo := RouteInfo{
				Route:            route,
				ClusterReference: clusterReference,
			}
			probe, found, err := r.findProbeForRoute(context, route, clusterClientMap[clusterReference])
			if err != nil {
				log.Error(err, "error finding probe", "for route", route, "for cluster", clusterReference)
				return r.ManageError(context, instance, err)
			}
			if found {
				routeInfo.ReadinessCheck = probe
			}
			service, err := r.findIngressControllerServiceForRoute(context, route, clusterClientMap[clusterReference])
			if err != nil {
				log.Error(err, "error finding service", "for route", route, "for cluster", clusterReference)
				return r.ManageError(context, instance, err)
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
					return r.ManageError(context, instance, err)
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
	//create or update GlobalDNSRecords
	for name, routeInfos := range routeRouteInfoSMap {
		globaDNSRecord, err := r.getGlobalDNSRecord(instance, name, routeInfos)
		if err != nil {
			log.Error(err, "unable to create global dns record", "for route infos", routeInfos)
			return r.ManageError(context, instance, err)
		}
		err = r.CreateOrUpdateResource(context, instance, instance.Namespace, globaDNSRecord)
		if err != nil {
			log.Error(err, "unable to create or update", "global dns record", globaDNSRecord)
			return r.ManageError(context, instance, err)
		}
	}

	return r.ManageSuccess(context, instance)
}

func (r *GlobalRouteDiscoveryReconciler) getGlobalDNSRecord(instance *redhatcopv1alpha1.GlobalRouteDiscovery, name string, routeInfos []RouteInfo) (*redhatcopv1alpha1.GlobalDNSRecord, error) {
	if len(routeInfos) == 0 {
		err := errs.New("no route info")
		r.Log.Error(err, "at least one route info must be passed", "routeInfos", routeInfos)
		return nil, err
	}
	r.Log.V(1).Info("route info array", "len", len(routeInfos))
	route0 := routeInfos[0]
	// consistency checks
	for i, route := range routeInfos {
		routeNumberString := fmt.Sprintf("route[%d]", i)
		if route.Route.Spec.Host != route0.Route.Spec.Host {
			err := errs.New("route's hosts must match")
			r.Log.Error(err, "route's host not matching", "route[0]", route0.Route.Spec.Host, routeNumberString, route.Route.Spec.Host)
			return nil, err
		}
		if route.LoadBalancigPolicy != route0.LoadBalancigPolicy {
			err := errs.New("route's load balancing policy must match")
			r.Log.Error(err, "route's load balancing policy not matching", "route[0]", route0.LoadBalancigPolicy, routeNumberString, route.LoadBalancigPolicy)
			return nil, err
		}
		if route.TTL != route0.TTL {
			err := errs.New("route's TTLs must match")
			r.Log.Error(err, "route's TTLs not matching", "route[0]", route0.TTL, routeNumberString, route.TTL)
			return nil, err
		}
		if !reflect.DeepEqual(route.ReadinessCheck, route0.ReadinessCheck) {
			err := errs.New("route's health checks must match")
			r.Log.Error(err, "route's health checks not matching", "route[0]", route0.ReadinessCheck, routeNumberString, route.ReadinessCheck)
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
		r.Log.V(1).Info("created", "endpoint", endpoint)
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

func (r *GlobalRouteDiscoveryReconciler) findIngressControllerServiceForRoute(context context.Context, route routev1.Route, c client.Client) (*corev1.Service, error) {
	if len(route.Status.Ingress) == 0 {
		err := errs.New("no status found")
		r.Log.Error(err, "route has no satus", "route", route)
		return &corev1.Service{}, err
	}
	service := &corev1.Service{}
	err := c.Get(context, types.NamespacedName{
		Namespace: "openshift-ingress",
		Name:      "router-" + route.Status.Ingress[0].RouterName,
	}, service)
	if err != nil {
		r.Log.Error(err, "unable to lookup ingress service", "for route", route)
		return &corev1.Service{}, err
	}
	return service, nil
}

func (r *GlobalRouteDiscoveryReconciler) findQualifyingRoutes(context context.Context, instance *redhatcopv1alpha1.GlobalRouteDiscovery, c client.Client) ([]routev1.Route, error) {
	routeList := &routev1.RouteList{}
	labelSelector, err := metav1.LabelSelectorAsSelector(&instance.Spec.RouteSelector)
	if err != nil {
		r.Log.Error(err, "unable to convert label selector to selector", "label selector", instance.Spec.RouteSelector)
		return []routev1.Route{}, err
	}
	err = c.List(context, routeList, &client.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		r.Log.Error(err, "unable to list routes ", "with label selector", labelSelector)
		return []routev1.Route{}, err
	}
	return routeList.Items, nil
}

func (r *GlobalRouteDiscoveryReconciler) findProbeForRoute(context context.Context, route routev1.Route, c client.Client) (*corev1.Probe, bool, error) {
	//first let't check if this route defines a healtch cehck via annotation
	if healthCheckString, ok := route.GetAnnotations()[healthCheckAnnotation]; ok {
		//let's try to unmarshal this string
		probe := &corev1.Probe{}
		err := json.Unmarshal([]byte(healthCheckString), probe)
		if err != nil {
			r.Log.Error(err, "unable to unmarshall", "corev1.probe", healthCheckString)
			return nil, false, err
		}
		return probe, true, nil
	}
	service := &corev1.Service{}
	err := c.Get(context, types.NamespacedName{
		Name:      route.Spec.To.Name,
		Namespace: route.Namespace,
	}, service)
	if err != nil {
		r.Log.Error(err, "unable to fund service associated to", "route", route)
		return &corev1.Probe{}, false, err
	}
	podList := &corev1.PodList{}
	labelSelector, err := metav1.LabelSelectorAsSelector(metav1.SetAsLabelSelector(service.Spec.Selector))
	if err != nil {
		r.Log.Error(err, "unable to convert label selector to selector", "label selector", service.Spec.Selector)
		return &corev1.Probe{}, false, err
	}
	err = c.List(context, podList, &client.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     service.Namespace,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &corev1.Probe{}, false, nil
		}
		r.Log.Error(err, "unable to list pods", "for service", service)
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

func (r *GlobalRouteDiscoveryReconciler) ensureRemoteManagers(context context.Context) error {
	instances := &redhatcopv1alpha1.GlobalRouteDiscoveryList{}
	err := r.GetClient().List(context, instances, &client.ListOptions{})
	if err != nil {
		r.Log.Error(err, "unable to list GlobalRouteDiscovery")
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
	for cluster := range r.remoteManagersMap {
		currentClusterReferenceSet.Add(cluster)
	}

	toBeAdded := strset.Difference(neededClusterReferenceSet, currentClusterReferenceSet)
	toBeRemoved := strset.Difference(currentClusterReferenceSet, neededClusterReferenceSet)

	r.Log.V(1).Info("to be added", "remote managers", toBeAdded)
	r.Log.V(1).Info("to be removed", "remote managers", toBeRemoved)

	for _, cluster := range toBeRemoved.List() {
		defer delete(r.remoteManagersMap, cluster)
		remoteManager, ok := r.remoteManagersMap[cluster]
		if !ok {
			continue
		}
		remoteManager.Stop()
	}

	for _, cluster := range toBeAdded.List() {
		remoteManager, err := r.createRemoteManager(context, neededClusterMap[cluster])
		if err != nil {
			r.Log.Error(err, "unable to create remote manager", "for cluster", cluster)
			return err
		}
		r.remoteManagersMap[cluster] = remoteManager
		remoteManager.Start()
	}
	r.Log.V(1).Info("remote manager map", "size", len(r.remoteManagersMap))
	return nil
}

func (r *GlobalRouteDiscoveryReconciler) createRemoteManager(context context.Context, cluster redhatcopv1alpha1.ClusterReference) (*remotemanager.RemoteManager, error) {
	restConfig, err := r.getRestConfig(context, cluster)
	if err != nil {
		r.Log.Error(err, "unable to create client for", "cluster", cluster)
		return nil, err
	}
	options := manager.Options{
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Scheme:             r.GetScheme(),
	}

	remoteManager, err := remotemanager.NewRemoteManager(restConfig, options, cluster.ClusterName+"@"+cluster.CredentialsSecretRef.Namespace+"/"+cluster.CredentialsSecretRef.Name)
	if err != nil {
		r.Log.Error(err, "unable to create stoppable manager for", "cluster", cluster)
		return nil, err
	}
	_, err = r.newRouteReconciler(remoteManager, r.reconcileEventChannel, cluster, r.GetClient())
	if err != nil {
		r.Log.Error(err, "unable create serviceReconciler", "for cluster", cluster)
		return nil, err
	}
	return remoteManager, nil
}

func (r *GlobalRouteDiscoveryReconciler) getRestConfig(context context.Context, cluster redhatcopv1alpha1.ClusterReference) (*rest.Config, error) {
	// for now we assume that we will have a secret with a kubeconfig.
	// the clustr ref is not really needed in this case.

	secret := &corev1.Secret{}

	err := r.GetClient().Get(context, types.NamespacedName{
		Name:      cluster.CredentialsSecretRef.Name,
		Namespace: cluster.CredentialsSecretRef.Namespace,
	}, secret)

	if err != nil {
		r.Log.Error(err, "unable to find", "secret", cluster.CredentialsSecretRef)
		return nil, err
	}

	kubeconfig, ok := secret.Data["kubeconfig"]

	if !ok {
		err := errs.New("unable to find kubeconfig key in secret")
		r.Log.Error(err, "", "secret", cluster.CredentialsSecretRef)
		return nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		r.Log.Error(err, "unable to create rest config", "kubeconfig", kubeconfig)
		return nil, err
	}

	return restConfig, nil

}

//ManageError manage error sets an error status in the CR and fires an event, finally it returns the error so the operator can re-attempt
func (r *GlobalRouteDiscoveryReconciler) ManageError(context context.Context, instance *redhatcopv1alpha1.GlobalRouteDiscovery, issue error) (reconcile.Result, error) {
	r.Log.V(1).Info("manage error called")
	r.GetRecorder().Event(instance, "Warning", "ProcessingError", issue.Error())
	condition := metav1.Condition{
		Type:               apis.ReconcileError,
		LastTransitionTime: metav1.Now(),
		Message:            issue.Error(),
		Reason:             apis.ReconcileErrorReason,
		Status:             metav1.ConditionTrue,
	}
	instance.Status.Conditions = apis.AddOrReplaceCondition(condition, instance.Status.Conditions)
	r.Log.V(1).Info("getting cluster referece statuses")
	instance.Status.ClusterReferenceStatuses = r.getClusterReferenceStatuses(instance, r.GetRecorder())
	r.Log.V(1).Info("about to modify state for", "instance version", instance.GetResourceVersion())
	err := r.GetClient().Status().Update(context, instance)
	if err != nil {
		if errors.IsResourceExpired(err) {
			r.Log.Info("unable to update status for", "object version", instance.GetResourceVersion(), "resource version expired, will trigger another reconcile cycle", "")
		} else {
			r.Log.Error(err, "unable to update status for", "object", instance)
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, issue
}

func (r *GlobalRouteDiscoveryReconciler) getClusterReferenceStatuses(instance *redhatcopv1alpha1.GlobalRouteDiscovery, recorder record.EventRecorder) map[string]apis.Conditions {
	r.Log.V(1).Info("entering getClusterReferenceStatuses")
	conditions := map[string]apis.Conditions{}
	r.Log.V(1).Info("remoteManagersMap", "len", len(r.remoteManagersMap))
	for _, clusterReference := range instance.Spec.Clusters {
		r.Log.V(1).Info("examining", "cluster", clusterReference)
		remoteManager, ok := r.remoteManagersMap[clusterReference.GetKey()]
		if ok {
			r.Log.V(1).Info("found", "remote manager", remoteManager, "status", remoteManager.GetStatus())
			conditions[remoteManager.GetKey()] = remoteManager.GetStatus()
			if condition, ok := apis.GetLastCondition(conditions[remoteManager.GetKey()]); ok && condition.Type == apis.ReconcileError && condition.Status == metav1.ConditionTrue {
				recorder.Event(instance, "Warning", "ProcessingError", condition.Message)
			}
		}
	}
	r.Log.V(1).Info("retrieved statuses", "conditions", conditions)
	return conditions
}

// ManageSuccess will update the status of the CR and return a successful reconcile result
func (r *GlobalRouteDiscoveryReconciler) ManageSuccess(context context.Context, instance *redhatcopv1alpha1.GlobalRouteDiscovery) (reconcile.Result, error) {
	//log.V(1).Info("manage success called")
	condition := metav1.Condition{
		Type:               apis.ReconcileSuccess,
		LastTransitionTime: metav1.Now(),
		Reason:             apis.ReconcileSuccessReason,
		Status:             metav1.ConditionTrue,
	}
	instance.Status.Conditions = apis.AddOrReplaceCondition(condition, instance.Status.Conditions)
	//log.V(1).Info("getting cluster reference statuses")
	instance.Status.ClusterReferenceStatuses = r.getClusterReferenceStatuses(instance, r.GetRecorder())
	//log.V(1).Info("about to modify state for", "instance version", instance.GetResourceVersion())
	//log.V(1).Info("about to update status", "instance", instance)
	err := r.GetClient().Status().Update(context, instance)
	if err != nil {
		if errors.IsResourceExpired(err) {
			r.Log.Info("unable to update status for", "object version", instance.GetResourceVersion(), "resource version expired, will trigger another reconcile cycle", "")
		} else {
			r.Log.Error(err, "unable to update status for", "object", instance)
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

//IsInitialized initislizes the instance, currently is simply adds a finalizer.
func (r *GlobalRouteDiscoveryReconciler) IsInitialized(obj metav1.Object) bool {
	isInitialized := true
	globalRouteDiscovery, ok := obj.(*redhatcopv1alpha1.GlobalRouteDiscovery)
	if !ok {
		r.Log.Error(errs.New("unable to convert to egressIPAM"), "unable to convert to egressIPAM")
		return false
	}
	if !util.HasFinalizer(globalRouteDiscovery, controllerName) {
		util.AddFinalizer(globalRouteDiscovery, controllerName)
		isInitialized = false
	}
	// if globalRouteDiscovery.Spec.DefaultLoadBalancingPolicy == "" {
	// 	globalRouteDiscovery.Spec.DefaultLoadBalancingPolicy = redhatcopv1alpha1.Multivalue
	// 	isInitialized = false
	// }
	// if globalRouteDiscovery.Spec.DefaultTTL == 0 {
	// 	globalRouteDiscovery.Spec.DefaultTTL = 60
	// 	isInitialized = false
	// }
	return isInitialized
}

// SetupWithManager sets up the controller with the Manager.
func (r *GlobalRouteDiscoveryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.remoteManagersMap = map[string]*remotemanager.RemoteManager{}
	r.reconcileEventChannel = make(chan event.GenericEvent)
	r.apireader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		For(&redhatcopv1alpha1.GlobalRouteDiscovery{
			TypeMeta: metav1.TypeMeta{
				Kind: "GlobalRouteDiscovery",
			},
		}, builder.WithPredicates(util.ResourceGenerationOrFinalizerChangedPredicate{})).
		Owns(&redhatcopv1alpha1.GlobalDNSRecord{
			TypeMeta: metav1.TypeMeta{
				Kind: "GlobalDNSRecord",
			},
		}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&source.Channel{Source: r.reconcileEventChannel}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

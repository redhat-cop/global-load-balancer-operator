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

package globaldnsrecord

import (
	"context"
	errs "errors"
	"strings"

	"github.com/go-logr/logr"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/controllers/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	"github.com/scylladb/go-set/strset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/external-dns/endpoint"
)

// GlobalDNSRecordReconciler reconciles a GlobalDNSRecord object
type GlobalDNSRecordReconciler struct {
	util.ReconcilerBase
	Log                   logr.Logger
	apireader             client.Reader
	controllerName        string
	remoteManagersMap     map[string]*remotemanager.RemoteManager
	reconcileEventChannel chan event.GenericEvent
}

// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globaldnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globaldnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globaldnsrecords/finalizers,verbs=update
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GlobalDNSRecord object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *GlobalDNSRecordReconciler) Reconcile(context context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("globaldnsrecord", req.NamespacedName)

	// your logic here

	// Fetch the GlobalDNSRecord instance
	instance := &redhatcopv1alpha1.GlobalDNSRecord{}
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

	endpointStatusMap := map[string]EndpointStatus{}
	// retrieve global zone

	globalZone := &redhatcopv1alpha1.GlobalDNSZone{}
	err = r.GetClient().Get(context, types.NamespacedName{
		Name: instance.Spec.GlobalZoneRef.Name,
	}, globalZone)

	if err != nil {
		log.Error(err, "unable to find referred ", "globalzone", types.NamespacedName{
			Name: instance.Spec.GlobalZoneRef.Name,
		})
		return r.ManageError(context, instance, endpointStatusMap, err)
	}

	if ok := r.IsInitialized(instance); !ok {
		err := r.GetClient().Update(context, instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(context, instance, endpointStatusMap, err)
		}
		return reconcile.Result{}, nil
	}

	if util.IsBeingDeleted(instance) {
		if !util.HasFinalizer(instance, r.controllerName) {
			return reconcile.Result{}, nil
		}
		err := r.manageCleanUpLogic(context, instance, globalZone)
		if err != nil {
			log.Error(err, "unable to delete instance", "instance", instance.GetName())
			return r.ManageError(context, instance, endpointStatusMap, err)
		}
		util.RemoveFinalizer(instance, r.controllerName)
		err = r.GetClient().Update(context, instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(context, instance, endpointStatusMap, err)
		}
		return reconcile.Result{}, nil
	}

	log.V(1).Info("found global zone")

	if !strings.HasSuffix(instance.Spec.Name, globalZone.Spec.Domain) {
		err := errs.New("global record does not belogn to domain")
		log.Error(err, "global", "record", instance.Spec.Name, "does not belong to domain", globalZone.Spec.Domain)
		return r.ManageError(context, instance, endpointStatusMap, err)
	}

	err = r.ensureRemoteManagers(context)
	if err != nil {
		log.Error(err, "unable to ensure remote manager are correctly configured")
		return r.ManageError(context, instance, endpointStatusMap, err)
	}

	for _, endpoint := range instance.Spec.Endpoints {
		endpointStatus, err := r.getEndPointStatus(context, endpoint)
		if err != nil {
			log.Error(err, "unable to retrieve endpoint status for", "endpoint", endpoint)
			endpointStatus = &EndpointStatus{
				err:      err,
				endpoint: endpoint,
			}
		}
		endpointStatusMap[endpoint.GetKey()] = *endpointStatus
	}

	// verify ability to create desired records, given the combinations of DNS implementation, loadbalancer type, remote cluster infrastructure type, healthchecks

	if globalZone.Spec.Provider.ExternalDNS != nil {
		return r.createExternalDNSRecord(context, instance, globalZone, endpointStatusMap)
	}

	if globalZone.Spec.Provider.Route53 != nil {
		return r.createRoute53Record(context, instance, globalZone, endpointStatusMap)
	}

	// if able create record.

	return r.ManageSuccess(context, instance, endpointStatusMap)
}

// func GetEndpointKey(endpoint redhatcopv1alpha1.Endpoint) string {
// 	return endpoint.ClusterName + "#" + endpoint.LoadBalancerServiceRef.Namespace + "/" + endpoint.LoadBalancerServiceRef.Name
// }

func (r *GlobalDNSRecordReconciler) createRemoteManager(context context.Context, endpoint redhatcopv1alpha1.Endpoint) (*remotemanager.RemoteManager, error) {
	restConfig, err := r.getRestConfig(context, endpoint)
	if err != nil {
		r.Log.Error(err, "unable to create client for", "endpoint", endpoint)
		return nil, err
	}
	options := manager.Options{
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Scheme:             r.GetScheme(),
	}

	remoteManager, err := remotemanager.NewRemoteManager(restConfig, options, endpoint.GetKey())
	if err != nil {
		r.Log.Error(err, "unable to create stoppable manager for", "endpoint", endpoint)
		return nil, err
	}
	_, err = newServiceReconciler(remoteManager, r.reconcileEventChannel, endpoint, r.GetClient())
	if err != nil {
		r.Log.Error(err, "unable create serviceReconciler", "for endpoint", endpoint)
		return nil, err
	}
	return remoteManager, nil
}

//ManageError manage error sets an error status in the CR and fires an event, finally it returns the error so the operator can re-attempt
func (r *GlobalDNSRecordReconciler) ManageError(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, endpointStatusMap map[string]EndpointStatus, issue error) (reconcile.Result, error) {
	r.GetRecorder().Event(instance, "Warning", "ProcessingError", issue.Error())
	condition := metav1.Condition{
		Type:               apis.ReconcileError,
		LastTransitionTime: metav1.Now(),
		Message:            issue.Error(),
		Reason:             apis.ReconcileErrorReason,
		Status:             metav1.ConditionTrue,
	}
	instance.Status.Conditions = apis.AddOrReplaceCondition(condition, instance.Status.Conditions)
	instance.Status.MonitoredServiceStatuses = r.getMonitoredServiceStatuses(instance, r.GetRecorder())
	instance.Status.EndpointStatuses = getEndpointStatuses(instance, endpointStatusMap, r.GetRecorder())
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

// ManageSuccess will update the status of the CR and return a successful reconcile result
func (r *GlobalDNSRecordReconciler) ManageSuccess(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, endpointStatusMap map[string]EndpointStatus) (reconcile.Result, error) {
	condition := metav1.Condition{
		Type:               apis.ReconcileSuccess,
		LastTransitionTime: metav1.Now(),
		Reason:             apis.ReconcileSuccessReason,
		Status:             metav1.ConditionTrue,
	}
	instance.Status.Conditions = apis.AddOrReplaceCondition(condition, instance.Status.Conditions)
	instance.Status.MonitoredServiceStatuses = r.getMonitoredServiceStatuses(instance, r.GetRecorder())
	instance.Status.EndpointStatuses = getEndpointStatuses(instance, endpointStatusMap, r.GetRecorder())
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
func (r *GlobalDNSRecordReconciler) IsInitialized(obj metav1.Object) bool {
	isInitialized := true
	globalDNSRecord, ok := obj.(*redhatcopv1alpha1.GlobalDNSRecord)
	if !ok {
		r.Log.Error(errs.New("unable to convert to egressIPAM"), "unable to convert to egressIPAM")
		return false
	}
	if !util.HasFinalizer(globalDNSRecord, r.controllerName) {
		util.AddFinalizer(globalDNSRecord, r.controllerName)
		isInitialized = false
	}
	if globalDNSRecord.Spec.TTL == 0 {
		globalDNSRecord.Spec.TTL = 60
		isInitialized = false
	}
	if globalDNSRecord.Spec.HealthCheck != nil {
		probe := globalDNSRecord.Spec.HealthCheck
		if probe.FailureThreshold == 0 {
			probe.FailureThreshold = 3
			isInitialized = false
		}
		if probe.PeriodSeconds == 0 {
			probe.PeriodSeconds = 10
			isInitialized = false
		}
		if probe.SuccessThreshold == 0 {
			probe.SuccessThreshold = 1
			isInitialized = false
		}
		if probe.TimeoutSeconds == 0 {
			probe.SuccessThreshold = 1
			isInitialized = false
		}
	}
	return isInitialized
}

func (r *GlobalDNSRecordReconciler) manageCleanUpLogic(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone) error {

	err := r.ensureRemoteManagers(context)
	if err != nil {
		r.Log.Error(err, "unable to ensure correct remote managers")
		return err
	}

	// provider specific finalizer

	if globalzone.Spec.Provider.ExternalDNS != nil {
		//nothing to do here because for the ownership rule, the DNSEndpoint record will be deleted and the external-dns operator will clean up the DNS configuration
		return nil
	}
	if globalzone.Spec.Provider.Route53 != nil {
		//we need to delete policy instance
		//health check
		//policy
		err := r.cleanUpRoute53DNSRecord(context, instance, globalzone)
		if err != nil {
			r.Log.Error(err, "unable to cleanup route53 dns record", "record", instance)
			return err
		}
		return nil
	}
	return errs.New("illegal state")
}

func (r *GlobalDNSRecordReconciler) ensureRemoteManagers(context context.Context) error {
	instances := &redhatcopv1alpha1.GlobalDNSRecordList{}
	err := r.GetClient().List(context, instances, &client.ListOptions{})
	if err != nil {
		r.Log.Error(err, "unable to list GlobalDNSRecord")
		return err
	}
	neededEndointMap := map[string]redhatcopv1alpha1.Endpoint{}
	neededEndPointSet := strset.New()
	for _, instance := range instances.Items {
		if !util.IsBeingDeleted(&instance) {
			for _, endpoint := range instance.Spec.Endpoints {
				neededEndPointSet.Add(endpoint.GetKey())
				neededEndointMap[endpoint.GetKey()] = endpoint
			}
		}
	}

	currentEndpointSet := strset.New()
	for cluster := range r.remoteManagersMap {
		currentEndpointSet.Add(cluster)
	}

	toBeAdded := strset.Difference(neededEndPointSet, currentEndpointSet)
	toBeRemoved := strset.Difference(currentEndpointSet, neededEndPointSet)

	r.Log.V(1).Info("to be added", "remote managers", toBeAdded)
	r.Log.V(1).Info("to be removed", "remote managers", toBeRemoved)

	for _, endpoint := range toBeRemoved.List() {
		defer delete(r.remoteManagersMap, endpoint)
		remoteManager, ok := r.remoteManagersMap[endpoint]
		if !ok {
			continue
		}
		remoteManager.Stop()
	}

	for _, endpoint := range toBeAdded.List() {
		remoteManager, err := r.createRemoteManager(context, neededEndointMap[endpoint])
		if err != nil {
			r.Log.Error(err, "unable to create remote manager", "for cluster", endpoint)
			return err
		}
		r.remoteManagersMap[endpoint] = remoteManager
		remoteManager.Start()
	}
	r.Log.V(1).Info("remote manager map", "size", len(r.remoteManagersMap))
	return nil
}

func (r *GlobalDNSRecordReconciler) getMonitoredServiceStatuses(instance *redhatcopv1alpha1.GlobalDNSRecord, recorder record.EventRecorder) map[string]apis.Conditions {
	conditions := map[string]apis.Conditions{}
	endpointManagerMap := map[string]*remotemanager.RemoteManager{}

	for _, endpoint := range instance.Spec.Endpoints {
		remoteManager, ok := r.remoteManagersMap[endpoint.GetKey()]
		if ok {
			endpointManagerMap[endpoint.GetKey()] = remoteManager
		}
	}

	for _, endpointManager := range endpointManagerMap {
		conditions[endpointManager.GetKey()] = endpointManager.GetStatus()

		if condition, ok := apis.GetLastCondition(conditions[endpointManager.GetKey()]); ok && condition.Type == apis.ReconcileError && condition.Status == metav1.ConditionTrue {
			recorder.Event(instance, "Warning", "ProcessingError", condition.Message)
		}
	}
	return conditions
}

// SetupWithManager sets up the controller with the Manager.
func (r *GlobalDNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.controllerName = "GlobalDNSRecord_controller"
	r.remoteManagersMap = map[string]*remotemanager.RemoteManager{}
	r.reconcileEventChannel = make(chan event.GenericEvent)
	r.apireader = mgr.GetAPIReader()
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&redhatcopv1alpha1.GlobalDNSRecord{
			TypeMeta: metav1.TypeMeta{
				Kind: "GlobalDNSRecord",
			},
		}, builder.WithPredicates(util.ResourceGenerationOrFinalizerChangedPredicate{})).
		Watches(&source.Channel{Source: r.reconcileEventChannel}, &handler.EnqueueRequestForObject{})

	if ok, _ := r.IsAPIResourceAvailable(schema.GroupVersionKind{
		Group:   "externaldns.k8s.io",
		Version: "v1alpha1",
		Kind:    "DNSEndpoint",
	}); ok {
		builder.Owns(&endpoint.DNSEndpoint{
			TypeMeta: metav1.TypeMeta{
				Kind: "DNSEndpoint",
			},
		})
	}
	return builder.Complete(r)
}

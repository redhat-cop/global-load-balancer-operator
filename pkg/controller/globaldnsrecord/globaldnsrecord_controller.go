package globaldnsrecord

import (
	"context"
	errs "errors"
	"strings"

	astatus "github.com/operator-framework/operator-sdk/pkg/ansible/controller/status"
	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common/remotemanager"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/external-dns/endpoint"
)

const controllerName = "globaldnsrecord-controller"

var remoteManagersMap = map[string]*remotemanager.RemoteManager{}
var reconcileEventChannel chan event.GenericEvent = make(chan event.GenericEvent)

var log = logf.Log.WithName(controllerName)

// ReconcileGlobalDNSRecord reconciles a GlobalDNSRecord object
type ReconcileGlobalDNSRecord struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
	apireader client.Reader
}

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new GlobalDNSRecord Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileGlobalDNSRecord{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),
		apireader:      mgr.GetAPIReader(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource GlobalDNSRecord
	err = c.Watch(&source.Kind{Type: &redhatcopv1alpha1.GlobalDNSRecord{
		TypeMeta: metav1.TypeMeta{
			Kind: "GlobalDNSRecord",
		},
	}}, &handler.EnqueueRequestForObject{}, util.ResourceGenerationOrFinalizerChangedPredicate{})
	if err != nil {
		return err
	}

	//create a watch on DNSEdnpoint, useful only with the external-dns operator
	//This watch will be created only if DNSEnpoint exists

	reconcileGlobalDNSRecord, ok := r.(*ReconcileGlobalDNSRecord)
	if !ok {
		return errs.New("unable to convert to ReconcileGlobalDNSRecord")
	}
	if ok, err := reconcileGlobalDNSRecord.IsAPIResourceAvailable(schema.GroupVersionKind{
		Group:   "externaldns.k8s.io",
		Version: "v1alpha1",
		Kind:    "DNSEndpoint",
	}); ok {
		// Watch for changes to primary resource GlobalDNSRecord
		err = c.Watch(&source.Kind{Type: &endpoint.DNSEndpoint{
			TypeMeta: metav1.TypeMeta{
				Kind: "DNSEndpoint",
			},
		}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &redhatcopv1alpha1.GlobalDNSRecord{},
		})
		if err != nil {
			return err
		}
	}

	//cretae watch to receive events
	err = c.Watch(
		&source.Channel{Source: reconcileEventChannel},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}
	return nil

}

// blank assignment to verify that ReconcileGlobalDNSRecord implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileGlobalDNSRecord{}

// Reconcile reads that state of the cluster for a GlobalDNSRecord object and makes changes based on the state read
// and what is in the GlobalDNSRecord.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileGlobalDNSRecord) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling GlobalDNSRecord")

	// Fetch the GlobalDNSRecord instance
	instance := &redhatcopv1alpha1.GlobalDNSRecord{}
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

	endpointStatusMap := map[string]EndpointStatus{}
	// retrieve global zone

	globalZone := &redhatcopv1alpha1.GlobalDNSZone{}
	err = r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: instance.Spec.GlobalZoneRef.Name,
	}, globalZone)

	if err != nil {
		log.Error(err, "unable to find referred ", "globalzone", types.NamespacedName{
			Name: instance.Spec.GlobalZoneRef.Name,
		})
		return r.ManageError(instance, endpointStatusMap, err)
	}

	if ok := r.IsInitialized(instance); !ok {
		err := r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(instance, endpointStatusMap, err)
		}
		return reconcile.Result{}, nil
	}

	if util.IsBeingDeleted(instance) {
		if !util.HasFinalizer(instance, controllerName) {
			return reconcile.Result{}, nil
		}
		err := r.manageCleanUpLogic(instance, globalZone)
		if err != nil {
			log.Error(err, "unable to delete instance", "instance", instance.GetName())
			return r.ManageError(instance, endpointStatusMap, err)
		}
		util.RemoveFinalizer(instance, controllerName)
		err = r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(instance, endpointStatusMap, err)
		}
		return reconcile.Result{}, nil
	}

	log.V(1).Info("found global zone")

	if !strings.HasSuffix(instance.Spec.Name, globalZone.Spec.Domain) {
		err := errs.New("global record does not belogn to domain")
		log.Error(err, "global", "record", instance.Spec.Name, "does not belong to domain", globalZone.Spec.Domain)
		return r.ManageError(instance, endpointStatusMap, err)
	}

	err = r.ensureRemoteManagers()
	if err != nil {
		log.Error(err, "unable to ensure remote manager are correctly configured")
		return r.ManageError(instance, endpointStatusMap, err)
	}

	for _, endpoint := range instance.Spec.Endpoints {
		endpointStatus, err := r.getEndPointStatus(endpoint)
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
		return r.createExternalDNSRecord(instance, globalZone, endpointStatusMap)
	}

	if globalZone.Spec.Provider.Route53 != nil {
		return r.createRoute53Record(instance, globalZone, endpointStatusMap)
	}

	// if able create record.

	return r.ManageSuccess(instance, endpointStatusMap)
}

// func GetEndpointKey(endpoint redhatcopv1alpha1.Endpoint) string {
// 	return endpoint.ClusterName + "#" + endpoint.LoadBalancerServiceRef.Namespace + "/" + endpoint.LoadBalancerServiceRef.Name
// }

func (r *ReconcileGlobalDNSRecord) createRemoteManager(endpoint redhatcopv1alpha1.Endpoint) (*remotemanager.RemoteManager, error) {
	restConfig, err := r.getRestConfig(endpoint)
	if err != nil {
		log.Error(err, "unable to create client for", "endpoint", endpoint)
		return nil, err
	}
	options := manager.Options{
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Scheme:             r.GetScheme(),
	}

	remoteManager, err := remotemanager.NewRemoteManager(restConfig, options, endpoint.GetKey())
	if err != nil {
		log.Error(err, "unable to create stoppable manager for", "endpoint", endpoint)
		return nil, err
	}
	_, err = newServiceReconciler(remoteManager, reconcileEventChannel, endpoint, r.GetClient())
	if err != nil {
		log.Error(err, "unable create serviceReconciler", "for endpoint", endpoint)
		return nil, err
	}
	return remoteManager, nil
}

//ManageError manage error sets an error status in the CR and fires an event, finally it returns the error so the operator can re-attempt
func (r *ReconcileGlobalDNSRecord) ManageError(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointStatusMap map[string]EndpointStatus, issue error) (reconcile.Result, error) {
	r.GetRecorder().Event(instance, "Warning", "ProcessingError", issue.Error())
	condition := status.Condition{
		Type:               "ReconcileError",
		LastTransitionTime: metav1.Now(),
		Message:            issue.Error(),
		Reason:             astatus.FailedReason,
		Status:             corev1.ConditionTrue,
	}
	instance.Status.Conditions = status.NewConditions(condition)
	instance.Status.MonitoredServiceStatuses = getMonitoredServiceStatuses(instance, r.GetRecorder())
	instance.Status.EndpointStatuses = getEndpointStatuses(instance, endpointStatusMap, r.GetRecorder())
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

// ManageSuccess will update the status of the CR and return a successful reconcile result
func (r *ReconcileGlobalDNSRecord) ManageSuccess(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointStatusMap map[string]EndpointStatus) (reconcile.Result, error) {
	condition := status.Condition{
		Type:               "ReconcileSuccess",
		LastTransitionTime: metav1.Now(),
		Message:            astatus.SuccessfulMessage,
		Reason:             astatus.SuccessfulReason,
		Status:             corev1.ConditionTrue,
	}
	instance.Status.Conditions = status.NewConditions(condition)
	instance.Status.MonitoredServiceStatuses = getMonitoredServiceStatuses(instance, r.GetRecorder())
	instance.Status.EndpointStatuses = getEndpointStatuses(instance, endpointStatusMap, r.GetRecorder())
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
func (r *ReconcileGlobalDNSRecord) IsInitialized(obj metav1.Object) bool {
	isInitialized := true
	globalDNSRecord, ok := obj.(*redhatcopv1alpha1.GlobalDNSRecord)
	if !ok {
		log.Error(errs.New("unable to convert to egressIPAM"), "unable to convert to egressIPAM")
		return false
	}
	if !util.HasFinalizer(globalDNSRecord, controllerName) {
		util.AddFinalizer(globalDNSRecord, controllerName)
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

func (r *ReconcileGlobalDNSRecord) manageCleanUpLogic(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone) error {

	err := r.ensureRemoteManagers()
	if err != nil {
		log.Error(err, "unable to ensure correct remote managers")
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
		err := r.cleanUpRoute53DNSRecord(instance, globalzone)
		if err != nil {
			log.Error(err, "unable to cleanup route53 dns record", "record", instance)
			return err
		}
		return nil
	}
	return errs.New("illegal state")
}

func (r *ReconcileGlobalDNSRecord) ensureRemoteManagers() error {
	instances := &redhatcopv1alpha1.GlobalDNSRecordList{}
	err := r.GetClient().List(context.TODO(), instances, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list GlobalDNSRecord")
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
	for cluster := range remoteManagersMap {
		currentEndpointSet.Add(cluster)
	}

	toBeAdded := strset.Difference(neededEndPointSet, currentEndpointSet)
	toBeRemoved := strset.Difference(currentEndpointSet, neededEndPointSet)

	log.V(1).Info("to be added", "remote managers", toBeAdded)
	log.V(1).Info("to be removed", "remote managers", toBeRemoved)

	for _, endpoint := range toBeRemoved.List() {
		defer delete(remoteManagersMap, endpoint)
		remoteManager, ok := remoteManagersMap[endpoint]
		if !ok {
			continue
		}
		remoteManager.Stop()
	}

	for _, endpoint := range toBeAdded.List() {
		remoteManager, err := r.createRemoteManager(neededEndointMap[endpoint])
		if err != nil {
			log.Error(err, "unable to create remote manager", "for cluster", endpoint)
			return err
		}
		remoteManagersMap[endpoint] = remoteManager
		remoteManager.Start()
	}
	log.V(1).Info("remote manager map", "size", len(remoteManagersMap))
	return nil
}

func getMonitoredServiceStatuses(instance *redhatcopv1alpha1.GlobalDNSRecord, recorder record.EventRecorder) map[string]status.Conditions {
	conditions := map[string]status.Conditions{}
	endpointManagerMap := map[string]*remotemanager.RemoteManager{}

	for _, endpoint := range instance.Spec.Endpoints {
		remoteManager, ok := remoteManagersMap[endpoint.GetKey()]
		if ok {
			endpointManagerMap[endpoint.GetKey()] = remoteManager
		}
	}

	for _, endpointManager := range endpointManagerMap {
		conditions[endpointManager.GetKey()] = endpointManager.GetStatus()
		if conditions[endpointManager.GetKey()].GetCondition("ReconcileError") != nil && conditions[endpointManager.GetKey()].GetCondition("ReconcileError").Status == v1.ConditionTrue {
			recorder.Event(instance, "Warning", "ProcessingError", conditions[endpointManager.GetKey()].GetCondition("ReconcileError").Message)
		}
	}
	return conditions
}

package globaldnsrecord

import (
	"context"
	errs "errors"
	"strings"

	astatus "github.com/operator-framework/operator-sdk/pkg/ansible/controller/status"
	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

var reconcileEventChannel chan event.GenericEvent = make(chan event.GenericEvent)

var log = logf.Log.WithName(controllerName)

// ReconcileGlobalDNSRecord reconciles a GlobalDNSRecord object
type ReconcileGlobalDNSRecord struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
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

// this contains a map of endpoint keys and the relative controlling manager
type EndPointManagers map[string]*RemoteManager

// this containes a map of currently know DNSrecords and their map of endpoint managers
var TrackedEndpointMap = map[types.NamespacedName]EndPointManagers{}

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

	// [1] verify if we have a GlobalDNSRecord for this instance

	endpointManagerMap, ok := TrackedEndpointMap[request.NamespacedName]
	same := false

	if ok {
		// [1.2] if yes, verify if the watched service are the same
		log.V(1).Info("remote managers already exist")
		same = isSameServices(endpointManagerMap, instance)
		if !same {
			// [1.2.2] if no, stop the current watchers
			log.V(1).Info("remote managers are not the same")
			for _, stoppableManager := range endpointManagerMap {
				stoppableManager.Stop()
			}
			// [1.2.1] if yes, go to [3]
		} else {
			log.V(1).Info("remote managers are the same")
		}
		// [1.2] if no go to [2]
	} else {
		log.V(1).Info("remote managers do not exist")
	}

	if !ok || !same {
		// [2] for each cluster, create service watchers
		endpointManagerMap, err = r.createRemoteManagers(instance)
		log.V(1).Info("new remote managers created")
		if err != nil {
			log.Error(err, "unable to create remote managers")
			return r.ManageError(instance, endpointStatusMap, err)
		}
		for _, stoppableManager := range endpointManagerMap {
			stoppableManager.Start()
			log.V(1).Info("new remote managers started")
		}
		TrackedEndpointMap[request.NamespacedName] = endpointManagerMap
	}

	// at this point the remoteManagers are created correctly and this will start sending us events if any of the service changes.

	for _, endpoint := range instance.Spec.Endpoints {
		endpointStatus, err := r.getEndPointStatus(endpoint)
		if err != nil {
			log.Error(err, "unable to retrieve endpoint status for", "endpoint", endpoint)
			endpointStatus = &EndpointStatus{
				err:      err,
				endpoint: endpoint,
			}
		}
		endpointStatusMap[getEndpointKey(endpoint)] = *endpointStatus
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

func getEndpointKey(endpoint redhatcopv1alpha1.Endpoint) string {
	return endpoint.ClusterName + "#" + endpoint.LoadBalancerServiceRef.Namespace + "/" + endpoint.LoadBalancerServiceRef.Name
}

func (r *ReconcileGlobalDNSRecord) createManager(endpoint redhatcopv1alpha1.Endpoint, instance *redhatcopv1alpha1.GlobalDNSRecord) (*RemoteManager, error) {
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

	remoteManager, err := NewRemoteManager(restConfig, options, getEndpointKey(endpoint))
	if err != nil {
		log.Error(err, "unable to create stoppable manager for", "endpoint", endpoint)
		return nil, err
	}
	_, err = newServiceReconciler(remoteManager, reconcileEventChannel, endpoint, instance)
	if err != nil {
		log.Error(err, "unable create serviceReconciler", "for endpoint", endpoint)
		return nil, err
	}
	return remoteManager, nil
}

func (r *ReconcileGlobalDNSRecord) createRemoteManagers(instance *redhatcopv1alpha1.GlobalDNSRecord) (map[string]*RemoteManager, error) {
	managerMap := map[string]*RemoteManager{}
	for _, endpoint := range instance.Spec.Endpoints {
		stoppablemanager, err := r.createManager(endpoint, instance)
		if err != nil {
			log.Error(err, "unable to create manager for ", "endpoint", endpoint)
			return nil, err
		}
		managerMap[getEndpointKey(endpoint)] = stoppablemanager
	}
	return managerMap, nil
}

func isSameServices(endpointManagerMap EndPointManagers, instance *redhatcopv1alpha1.GlobalDNSRecord) (same bool) {
	currentRecord := strset.New()
	newRecord := strset.New()
	for key := range endpointManagerMap {
		currentRecord.Add(key)
	}
	for _, endpoint := range instance.Spec.Endpoints {
		newRecord.Add(getEndpointKey(endpoint))
	}
	same = currentRecord.IsEqual(newRecord)
	return same
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
	status := redhatcopv1alpha1.GlobalDNSRecordStatus{
		Conditions:               status.NewConditions(condition),
		MonitoredServiceStatuses: getMonitoredServiceStatuses(instance, r.GetRecorder()),
		EndpointStatuses:         getEndpointStatuses(instance, endpointStatusMap, r.GetRecorder()),
	}
	instance.Status = status
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
	status := redhatcopv1alpha1.GlobalDNSRecordStatus{
		Conditions:               status.NewConditions(condition),
		MonitoredServiceStatuses: getMonitoredServiceStatuses(instance, r.GetRecorder()),
		EndpointStatuses:         getEndpointStatuses(instance, endpointStatusMap, r.GetRecorder()),
	}
	instance.Status = status
	log.V(1).Info("about to modify state for", "instance version", instance.GetResourceVersion())
	log.V(1).Info("about to update status", "instance", instance)
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
	return isInitialized
}

func (r *ReconcileGlobalDNSRecord) manageCleanUpLogic(instance *redhatcopv1alpha1.GlobalDNSRecord, globalZone *redhatcopv1alpha1.GlobalDNSZone) error {
	// stop managers
	if endpointManagerMap, ok := TrackedEndpointMap[types.NamespacedName{
		Name:      instance.Name,
		Namespace: instance.Namespace,
	}]; ok {
		for _, remoteManager := range endpointManagerMap {
			remoteManager.Stop()
		}
	}

	// provider specific finalizer

	if globalZone.Spec.Provider.ExternalDNS != nil {
		//nothing to do here because for the ownership rule, the DNSEndpoint record will be deleted and the external-dns operator will clean up the DNS configuration
		return nil
	}
	return errs.New("illegal state")
}

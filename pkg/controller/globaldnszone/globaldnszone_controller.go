package globaldnszone

import (
	"context"
	errs "errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "globaldnszone-controller"

var log = logf.Log.WithName(controllerName)

// ReconcileGlobalDNSZone reconciles a GlobalDNSZone object
type ReconcileGlobalDNSZone struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
}

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new GlobalDNSZone Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileGlobalDNSZone{
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

	// Watch for changes to primary resource GlobalDNSZone
	err = c.Watch(&source.Kind{Type: &redhatcopv1alpha1.GlobalDNSZone{
		TypeMeta: metav1.TypeMeta{
			Kind: "GlobalDNSZone",
		},
	}}, &handler.EnqueueRequestForObject{}, util.ResourceGenerationOrFinalizerChangedPredicate{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileGlobalDNSZone implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileGlobalDNSZone{}

// Reconcile reads that state of the cluster for a GlobalDNSZone object and makes changes based on the state read
// and what is in the GlobalDNSZone.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileGlobalDNSZone) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling GlobalDNSZone")

	// Fetch the GlobalDNSZone instance
	instance := &redhatcopv1alpha1.GlobalDNSZone{}
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

	if ok, err := r.IsValid(instance); !ok {
		return r.ManageError(instance, err)
	}

	return r.ManageSuccess(instance)

}

//IsValid check if the instance is valid. In particular it checks that the CIDRs and the reservedIPs can be parsed correctly
func (r *ReconcileGlobalDNSZone) IsValid(obj metav1.Object) (bool, error) {
	globalDNSZone, ok := obj.(*redhatcopv1alpha1.GlobalDNSZone)
	if !ok {
		return false, errs.New("unable to convert to GlobalDNSZone")
	}
	foundProviderDefinition := false
	if globalDNSZone.Spec.Provider.Route53 != nil {
		if foundProviderDefinition {
			return false, errs.New("only one of the provider type field can be defined")
		}
		foundProviderDefinition = true
		// for route53 we just need to verify that the Zone is and that it controls the defined domain
		route53Client, err := r.getRoute53Client(globalDNSZone)
		if err != nil {
			log.Error(err, "unable to retrieve route53 client for", "globalDNSZone", globalDNSZone)
			return false, err
		}
		input := &route53.GetHostedZoneInput{
			Id: aws.String(globalDNSZone.Spec.Provider.Route53.ZoneID),
		}
		result, err := route53Client.GetHostedZone(input)
		if err != nil {
			log.Error(err, "unable to retrieve hosted zone with", "ZoneID", globalDNSZone.Spec.Provider.Route53.ZoneID)
			return false, err
		}
		if *result.HostedZone.Name != (globalDNSZone.Spec.Domain + ".") {
			err := errs.New("route53 hosted zone name does not match global dns name")
			log.Error(err, "aws route53 hosted zone", "name", result.HostedZone.Name, "does not match this global DNS zone name", globalDNSZone.Spec.Domain+".")
			return false, err
		}
	}
	return true, nil
}

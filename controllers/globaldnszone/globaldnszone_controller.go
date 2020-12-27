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

package globaldnszone

import (
	"context"
	errs "errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/go-logr/logr"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	tpdroute53 "github.com/redhat-cop/global-load-balancer-operator/controllers/common/route53"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// GlobalDNSZoneReconciler reconciles a GlobalDNSZone object
type GlobalDNSZoneReconciler struct {
	util.ReconcilerBase
	Log logr.Logger
}

// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globaldnszones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globaldnszones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=globaldnszones/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GlobalDNSZone object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *GlobalDNSZoneReconciler) Reconcile(context context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("globaldnszone", req.NamespacedName)

	// Fetch the GlobalDNSZone instance
	instance := &redhatcopv1alpha1.GlobalDNSZone{}
	err := r.GetClient().Get(context, req.NamespacedName, instance)
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
		return r.ManageError(context, instance, err)
	}

	return r.ManageSuccess(context, instance)

}

//IsValid check if the instance is valid. In particular it checks that the CIDRs and the reservedIPs can be parsed correctly
func (r *GlobalDNSZoneReconciler) IsValid(obj metav1.Object) (bool, error) {
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
		route53Client, err := tpdroute53.GetRoute53Client(context.TODO(), globalDNSZone, &r.ReconcilerBase)
		if err != nil {
			r.Log.Error(err, "unable to retrieve route53 client for", "globalDNSZone", globalDNSZone)
			return false, err
		}
		input := &route53.GetHostedZoneInput{
			Id: aws.String(globalDNSZone.Spec.Provider.Route53.ZoneID),
		}
		result, err := route53Client.GetHostedZone(input)
		if err != nil {
			r.Log.Error(err, "unable to retrieve hosted zone with", "ZoneID", globalDNSZone.Spec.Provider.Route53.ZoneID)
			return false, err
		}
		if *result.HostedZone.Name != (globalDNSZone.Spec.Domain + ".") {
			err := errs.New("route53 hosted zone name does not match global dns name")
			r.Log.Error(err, "aws route53 hosted zone", "name", result.HostedZone.Name, "does not match this global DNS zone name", globalDNSZone.Spec.Domain+".")
			return false, err
		}
	}
	return true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GlobalDNSZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redhatcopv1alpha1.GlobalDNSZone{
			TypeMeta: metav1.TypeMeta{
				Kind: "GlobalDNSZone",
			},
		}, builder.WithPredicates(util.ResourceGenerationOrFinalizerChangedPredicate{})).
		Complete(r)
}

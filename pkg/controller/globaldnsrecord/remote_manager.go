package globaldnsrecord

import (
	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
)

func getMonitoredServiceStatuses(instance *redhatcopv1alpha1.GlobalDNSRecord, recorder record.EventRecorder) map[string]status.Conditions {
	conditions := map[string]status.Conditions{}
	endpointManagerMap, ok := TrackedEndpointMap[types.NamespacedName{
		Name:      instance.Name,
		Namespace: instance.Namespace,
	}]
	if !ok {
		return nil
	}
	for _, endpointManager := range endpointManagerMap {
		conditions[endpointManager.GetKey()] = endpointManager.GetStatus()
		if conditions[endpointManager.GetKey()].GetCondition("ReconcileError") != nil && conditions[endpointManager.GetKey()].GetCondition("ReconcileError").Status == v1.ConditionTrue {
			recorder.Event(instance, "Warning", "ProcessingError", conditions[endpointManager.GetKey()].GetCondition("ReconcileError").Message)
		}
	}
	return conditions
}

package globaldnsrecord

import (
	"sync"

	"github.com/operator-framework/operator-sdk/pkg/status"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util/stoppablemanager"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type RemoteManager struct {
	stoppablemanager.StoppableManager
	status      status.Conditions
	statusMutex sync.Mutex
	key         string
}

func (rm *RemoteManager) getStatus() status.Conditions {
	rm.statusMutex.Lock()
	defer rm.statusMutex.Unlock()
	return rm.status
}

func (rm *RemoteManager) setStatus(status status.Conditions) {
	rm.statusMutex.Lock()
	defer rm.statusMutex.Unlock()
	rm.status = status
}

func NewRemoteManager(restConfig *rest.Config, options manager.Options, key string) (*RemoteManager, error) {
	stoppableManager, err := stoppablemanager.NewStoppableManager(restConfig, options)
	if err != nil {
		log.Error(err, "unable to create stoppablemanager")
		return nil, err
	}
	return &RemoteManager{
		StoppableManager: stoppableManager,
		status:           status.Conditions{},
		statusMutex:      sync.Mutex{},
		key:              key,
	}, nil
}

func (rm *RemoteManager) getKey() string {
	return rm.key
}

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
		conditions[endpointManager.getKey()] = endpointManager.getStatus()
		if conditions[endpointManager.getKey()].GetCondition("ReconcileError") != nil && conditions[endpointManager.getKey()].GetCondition("ReconcileError").Status == v1.ConditionTrue {
			recorder.Event(instance, "Warning", "ProcessingError", conditions[endpointManager.getKey()].GetCondition("ReconcileError").Message)
		}
	}
	return conditions
}

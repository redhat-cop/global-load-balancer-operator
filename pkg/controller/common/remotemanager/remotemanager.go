package remotemanager

import (
	"sync"

	"github.com/operator-framework/operator-sdk/pkg/status"
	"github.com/redhat-cop/operator-utils/pkg/util/stoppablemanager"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var log = logf.Log.WithName("remote-manager")

type RemoteManager struct {
	stoppablemanager.StoppableManager
	status      status.Conditions
	statusMutex sync.Mutex
	key         string
}

func (rm *RemoteManager) GetStatus() status.Conditions {
	rm.statusMutex.Lock()
	defer rm.statusMutex.Unlock()
	return rm.status
}

func (rm *RemoteManager) SetStatus(status status.Conditions) {
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

func (rm *RemoteManager) GetKey() string {
	return rm.key
}

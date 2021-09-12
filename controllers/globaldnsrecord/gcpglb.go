package globaldnsrecord

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	cgoogle "github.com/redhat-cop/global-load-balancer-operator/controllers/common/google"
	"github.com/scylladb/go-set/strset"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type googleGCPProvider struct {
	instance      *redhatcopv1alpha1.GlobalDNSRecord
	globalzone    *redhatcopv1alpha1.GlobalDNSZone
	endpointMap   map[string]EndpointStatus
	log           logr.Logger
	computeClient *compute.Service
	dnsClient     *dns.Service
	googleProject string
}

var _ globalDNSProvider = &googleGCPProvider{}

func (r *GlobalDNSRecordReconciler) createGCPGLBManagerProvider(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (*googleGCPProvider, error) {
	// validate that all infratsrcutre is homogeneus
	for _, endpointStatus := range endpointMap {
		if endpointStatus.infrastructure.Status.PlatformStatus.Type != ocpconfigv1.GCPPlatformType {
			err := errors.New("Illegal state, only gcp endpoints are allowed")
			r.Log.Error(err, "need gcp endpoint", "endpoint type", endpointStatus.infrastructure.Status.PlatformStatus.Type)
			return &googleGCPProvider{}, err
		}
	}

	if instance.Spec.HealthCheck == nil {
		return &googleGCPProvider{}, errors.New("missing healthcheck definition, heathchecks are mandatory in google")
	}

	projectID, computeClient, dnsClient, err := cgoogle.GetGCPClients(context, globalzone, &r.ReconcilerBase)
	if err != nil {
		r.Log.Error(err, "unable to get gcp client")
		return &googleGCPProvider{}, err
	}
	return &googleGCPProvider{
		instance:      instance,
		globalzone:    globalzone,
		endpointMap:   endpointMap,
		computeClient: computeClient,
		dnsClient:     dnsClient,
		log:           r.Log.WithName("GCPGLBProvider"),
		googleProject: projectID,
	}, nil
}

func (p *googleGCPProvider) deleteDNSRecord(context context.Context) error {

	// delete dns entry (delete if exists logic)
	response, err := p.dnsClient.ResourceRecordSets.Delete(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, p.instance.Spec.Name, "A").Context(context).Do()
	if err != nil && response.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "dns entry", p.instance.Spec.Name)
		return err
	}

	// delete forwarding rule
	op, err := p.computeClient.GlobalForwardingRules.Delete(p.googleProject, p.getForwardingRuleName()).Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "forwarding rule", p.getForwardingRuleName())
		return err
	}

	// delete global ip
	op, err = p.computeClient.GlobalAddresses.Delete(p.googleProject, p.getGlobalIPName()).Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getGlobalIPName())
		return err
	}

	// delete target tcp proxy
	op, err = p.computeClient.TargetTcpProxies.Delete(p.googleProject, p.getTargetTCPProxyName()).Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getTargetTCPProxyName())
		return err
	}

	// delete backend service
	op, err = p.computeClient.BackendServices.Delete(p.googleProject, p.getBackendServiceName()).Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getBackendServiceName())
		return err
	}

	// delete health check
	op, err = p.computeClient.HealthChecks.Delete(p.googleProject, p.getHealthCheckName()).Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getHealthCheckName())
		return err
	}

	// delete zonal network endpoint groups
	for name, endpoint := range p.endpointMap {
		desiredNepsByZone, err := p.getDesiredNepsByZone(context, &endpoint)
		if err != nil {
			p.log.Error(err, "unable to get selected nodes by zone for", "endpoint", name)
			return err
		}
		for zone := range desiredNepsByZone {
			op, err = p.computeClient.NetworkEndpointGroups.Delete(p.googleProject, zone, p.getZonalNetworkEndpointGroupName(zone)).Context(context).Do()
			if err != nil && op.HTTPStatusCode != 404 {
				p.log.Error(err, "unable to delete", "global address", p.getZonalNetworkEndpointGroupName(zone))
				return err
			}
		}
	}

	return nil
}

func (p *googleGCPProvider) ensureDNSRecord(context context.Context) error {

	// ensure firewall rules allow for health checks
	err := p.ensureFirewallRulesAllowForHealthChecks(context)
	if err != nil {
		p.log.Error(err, "unable to create/update firewall rules to allow for health checks")
		return err
	}

	// ensure health check
	healthCheckID, err := p.ensureHealthCheck(context)
	if err != nil {
		p.log.Error(err, "unable to create/update health check")
		return err
	}

	//ensure zonal network endpoint groups
	zneg, err := p.ensureZonalNetworkEndpointGroups(context)
	if err != nil {
		p.log.Error(err, "unable to create/update zonal network endpoint groups")
		return err
	}

	//ensure backend service
	backendServiceID, err := p.ensureBackendService(context, healthCheckID, zneg)
	if err != nil {
		p.log.Error(err, "unable to create/update backend service")
		return err
	}

	//ensure target tcp proxy
	targetTCPProxyID, err := p.ensureTargetTCPProxy(context, backendServiceID)
	if err != nil {
		p.log.Error(err, "unable to create/update target tcp proxy")
		return err
	}

	//ensure global ip
	globalIP, err := p.ensureGlobalIP(context)
	if err != nil {
		p.log.Error(err, "unable to create/update global ip")
		return err
	}

	//ensure forwarding rule
	err = p.ensureForwardingRule(context, targetTCPProxyID, globalIP)
	if err != nil {
		p.log.Error(err, "unable to create/update forwarding rule")
		return err
	}

	//ensure dns entry
	err = p.ensureDNSEntry(context, globalIP)
	if err != nil {
		p.log.Error(err, "unable to create/update dns entry")
		return err
	}

	return nil
}

func (p *googleGCPProvider) compareRecordSet(a, b *dns.ResourceRecordSet) bool {
	if !(a.Rrdatas != nil && b.Rrdatas != nil && a.Rrdatas[0] == b.Rrdatas[0]) {
		return false
	}
	return a.Type == b.Type && a.Ttl == b.Ttl
}

func (p *googleGCPProvider) getDesiredRecordSet(globalIP string) *dns.ResourceRecordSet {
	return &dns.ResourceRecordSet{
		Name:    p.getRecordSetName(),
		Type:    "A",
		Ttl:     int64(p.instance.Spec.TTL),
		Rrdatas: []string{globalIP},
	}
}

func (p *googleGCPProvider) ensureDNSEntry(context context.Context, globalIP string) error {
	desiredRecordSet := p.getDesiredRecordSet(globalIP)
	rrs, err := p.dnsClient.ResourceRecordSets.Get(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, p.getRecordSetName(), "A").Context(context).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code != 404 {
				p.log.Error(err, "unable to get", "dns record", p.getRecordSetName())
				return err
			} else {
				_, err := p.dnsClient.ResourceRecordSets.Create(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, desiredRecordSet).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create", "dns entry", p.getRecordSetName(), "ip", globalIP)
					return err
				}
			}
		}
	} else {
		if !p.compareRecordSet(rrs, desiredRecordSet) {
			_, err = p.dnsClient.ResourceRecordSets.Patch(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, p.getRecordSetName(), "A", desiredRecordSet).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to update", "dns entry", p.getRecordSetName(), "ip", globalIP)
				return err
			}
		}
	}
	return nil
}

func (p *googleGCPProvider) compareBackendServices(a, b *compute.BackendService) bool {
	if !(a.HealthChecks != nil && b.HealthChecks != nil && a.HealthChecks[0] == b.HealthChecks[0]) {
		return false
	}
	aznegsIds := []string{}
	bznegsIds := []string{}
	for _, zneg := range a.Backends {
		aznegsIds = append(aznegsIds, zneg.Group)
	}
	for _, zneg := range b.Backends {
		bznegsIds = append(bznegsIds, zneg.Group)
	}
	if !strset.New(aznegsIds...).IsEqual(strset.New(bznegsIds...)) {
		return false
	}
	return a.Protocol == b.Protocol && a.LoadBalancingScheme == b.LoadBalancingScheme
}

func (p *googleGCPProvider) getDesiredBackendService(healthCheckID string, znegs []*compute.Backend) *compute.BackendService {
	return &compute.BackendService{
		Protocol:            "TCP",
		LoadBalancingScheme: "EXTERNAL",
		HealthChecks:        []string{healthCheckID},
		Backends:            znegs,
		Name:                p.getBackendServiceName(),
	}
}

func (p *googleGCPProvider) ensureBackendService(context context.Context, healthCheckID string, znegs []*compute.Backend) (string, error) {
	name := p.getBackendServiceName()
	desiredBackendService := p.getDesiredBackendService(healthCheckID, znegs)
	backendService, err := p.computeClient.BackendServices.Get(p.googleProject, name).Context(context).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code != 404 {
				p.log.Error(err, "unable to get", "backendsevice", name)
				return "", err
			} else {
				op, err := p.computeClient.BackendServices.Insert(p.googleProject, desiredBackendService).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create", "backend service", name)
					return "", err
				}
				return op.TargetLink, nil
			}
		}
	} else {
		if !p.compareBackendServices(backendService, desiredBackendService) {
			op, err := p.computeClient.BackendServices.Update(p.googleProject, name, desiredBackendService).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to update", "backend service", p.getBackendServiceName())
				return "", err
			}
			return op.TargetLink, nil
		}
	}
	return backendService.SelfLink, nil
}

func (p *googleGCPProvider) compareForwardingRule(a, b *compute.ForwardingRule) bool {
	if !(a.Ports != nil && b.Ports != nil && a.Ports[0] == b.Ports[0] && a.Ports[1] == b.Ports[1]) {
		return false
	}
	return a.IPProtocol == b.IPProtocol && a.IPAddress == b.IPAddress && a.LoadBalancingScheme == b.LoadBalancingScheme && a.Target == b.Target
}

func (p *googleGCPProvider) getDesiredForwardingRule(targetTCPProxyID string, globalIP string) (*compute.ForwardingRule, error) {
	var port string
	switch p.instance.Spec.HealthCheck.HTTPGet.Scheme {
	case "HTTPS":
		port = "443"
	case "HTTP":
		port = "80"
	default:
		return &compute.ForwardingRule{}, errors.New("unsupported healthcheck scheme")
	}
	return &compute.ForwardingRule{
		IPProtocol:          "TCP",
		IPAddress:           globalIP,
		LoadBalancingScheme: "EXTERNAL",
		PortRange:           port + "-" + port,
		Target:              targetTCPProxyID,
		Name:                p.getForwardingRuleName(),
	}, nil
}

func (p *googleGCPProvider) ensureForwardingRule(context context.Context, targetTCPProxyID string, globalIP string) error {
	name := p.getForwardingRuleName()
	desiredForwardingRule, err := p.getDesiredForwardingRule(targetTCPProxyID, globalIP)
	if err != nil {
		p.log.Error(err, "unable to figure out desired forwarding rule")
		return err
	}
	forwardingRule, err := p.computeClient.GlobalForwardingRules.Get(p.googleProject, name).Context(context).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code != 404 {
				p.log.Error(err, "unable to get", "forwardingRule", name)
				return err
			} else {
				_, err := p.computeClient.GlobalForwardingRules.Insert(p.googleProject, desiredForwardingRule).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create global", "forwarding rule", name)
					return err
				}
				return nil
			}
		}
	} else {
		if !p.compareForwardingRule(forwardingRule, desiredForwardingRule) {
			_, err := p.computeClient.GlobalForwardingRules.Patch(p.googleProject, name, desiredForwardingRule).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to patch", "forwarding rule", name)
				return err
			}
		}
	}
	return nil
}

func (p *googleGCPProvider) compareTargetTCPProxy(a, b *compute.TargetTcpProxy) bool {
	return a.Service == b.Service
}

func (p *googleGCPProvider) getDesiredTargetTCPProxy(backendID string) *compute.TargetTcpProxy {
	return &compute.TargetTcpProxy{
		Name:    p.getTargetTCPProxyName(),
		Service: backendID,
	}
}

func (p *googleGCPProvider) ensureTargetTCPProxy(context context.Context, backendID string) (string, error) {
	name := p.getTargetTCPProxyName()
	desiredTargetTCPProxy := p.getDesiredTargetTCPProxy(backendID)
	var targetTCPProxyID string
	targetTCPProxy, err := p.computeClient.TargetTcpProxies.Get(p.googleProject, name).Context(context).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code != 404 {
				p.log.Error(err, "unable to get", "targetTCPProxy", name)
				return "", err
			} else {
				op, err := p.computeClient.TargetTcpProxies.Insert(p.googleProject, desiredTargetTCPProxy).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create", "targetTCPProxy", name)
					return "", err
				}
				targetTCPProxyID = op.TargetLink
			}
		}
	} else {
		if !p.compareTargetTCPProxy(targetTCPProxy, desiredTargetTCPProxy) {
			_, err = p.computeClient.TargetTcpProxies.SetBackendService(p.googleProject, name, &compute.TargetTcpProxiesSetBackendServiceRequest{
				Service: backendID,
			}).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to set backend service", "targetTCPProxyID", targetTCPProxyID, "backendID", backendID)
				return "", err
			}
		}
		targetTCPProxyID = targetTCPProxy.SelfLink
	}
	return targetTCPProxyID, nil
}

func (p *googleGCPProvider) ensureGlobalIP(context context.Context) (string, error) {
	name := p.getGlobalIPName()
	var IP string
	address, err := p.computeClient.GlobalAddresses.Get(p.googleProject, name).Context(context).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code != 404 {
				p.log.Error(err, "unable to get", "address", name)
				return "", err
			} else {
				//we have to create the global ip
				_, err := p.computeClient.GlobalAddresses.Insert(p.googleProject, &compute.Address{
					IpVersion: "IPV4",
					Name:      name,
				}).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create global address")
					return "", err
				}
				//apparently we have to get it to know the IP that was assigned...
				address, err := p.computeClient.GlobalAddresses.Get(p.googleProject, name).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to get", "address", name)
					return "", err
				}
				IP = address.Address
			}

		}
	} else {
		IP = address.Address
	}
	return IP, nil
}

func (p *googleGCPProvider) getDesiredNepsByZone(context context.Context, endpoint *EndpointStatus) (map[string][]compute.NetworkEndpoint, error) {
	//retrieve node selector
	service := &corev1.Service{}
	err := endpoint.client.Get(context, types.NamespacedName{
		Namespace: endpoint.endpoint.LoadBalancerServiceRef.Namespace,
		Name:      endpoint.endpoint.LoadBalancerServiceRef.Name,
	}, service)
	if err != nil {
		p.log.Error(err, "unable to get", "service", types.NamespacedName{
			Namespace: endpoint.endpoint.LoadBalancerServiceRef.Namespace,
			Name:      endpoint.endpoint.LoadBalancerServiceRef.Name,
		})
		return map[string][]compute.NetworkEndpoint{}, err
	}
	var httpsPort int32
	var httpPort int32
	for _, port := range service.Spec.Ports {
		switch port.Name {
		case "http":
			httpPort = port.NodePort
		case "https":
			httpsPort = port.NodePort
		}
	}
	if httpsPort == 0 || httpPort == 0 {
		err := errors.New("unable to retrieve ports from service")
		p.log.Error(err, "unable to retrieve ports from service")
		return map[string][]compute.NetworkEndpoint{}, err
	}
	pods := &corev1.PodList{}
	err = endpoint.client.List(context, pods, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(service.Spec.Selector),
		Namespace:     endpoint.endpoint.LoadBalancerServiceRef.Namespace,
	})
	if err != nil {
		p.log.Error(err, "unable to get pods with ", "label selector", service.Spec.Selector, "namespace", endpoint.endpoint.LoadBalancerServiceRef.Namespace)
		return map[string][]compute.NetworkEndpoint{}, err
	}
	nodeSelector := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}
	if len(pods.Items) > 0 || pods.Items[0].Spec.NodeSelector != nil {
		nodeSelector = pods.Items[0].Spec.NodeSelector
	}
	//retrieve nodes
	nodes := &corev1.NodeList{}
	err = endpoint.client.List(context, nodes, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(nodeSelector),
	})
	if err != nil {
		p.log.Error(err, "unable to list nodes with", "selector", nodeSelector)
		return map[string][]compute.NetworkEndpoint{}, err
	}
	nepByZone := map[string][]compute.NetworkEndpoint{}
	for _, node := range nodes.Items {
		instanceName := strings.Split(node.Spec.ProviderID, "/")[len(strings.Split(node.Spec.ProviderID, "/"))-1]
		switch p.instance.Spec.HealthCheck.HTTPGet.Scheme {
		case "HTTPS":
			nepByZone[node.Labels["topology.kubernetes.io/zone"]] = append(nepByZone[node.Labels["topology.kubernetes.io/zone"]], compute.NetworkEndpoint{
				Instance: instanceName,
				Port:     int64(httpsPort),
			})

		case "HTTP":
			nepByZone[node.Labels["topology.kubernetes.io/zone"]] = append(nepByZone[node.Labels["topology.kubernetes.io/zone"]], compute.NetworkEndpoint{
				Instance: instanceName,
				Port:     int64(httpPort),
			})
		}
	}
	return nepByZone, nil
}

func (p *googleGCPProvider) ensureZonalNetworkEndpointGroups(context context.Context) ([]*compute.Backend, error) {
	backends := []*compute.Backend{}
	for name, endpoint := range p.endpointMap {
		network := "projects/" + p.googleProject + "/global/networks/" + endpoint.infrastructure.Status.InfrastructureName + "-network"
		subnetwork := "projects/" + p.googleProject + "/regions/" + endpoint.infrastructure.Status.PlatformStatus.GCP.Region + "/subnetworks/" + endpoint.infrastructure.Status.InfrastructureName + "-worker-subnet"
		desiredNepsByZone, err := p.getDesiredNepsByZone(context, &endpoint)
		if err != nil {
			p.log.Error(err, "unable to get selected nodes by zone for", "endpoint", name)
			return []*compute.Backend{}, err
		}
		for zone, desiredNeps := range desiredNepsByZone {
			// verify if network endpoint group already exists
			var znepgID string
			var needsCreation bool
			znepgName := p.getZonalNetworkEndpointGroupName(zone)
			znepg, err := p.computeClient.NetworkEndpointGroups.Get(p.googleProject, zone, znepgName).Context(context).Do()
			if err != nil {
				if e, ok := err.(*googleapi.Error); ok {
					if e.Code != 404 {
						p.log.Error(err, "unable to get zonal network endpoint group", "zone", zone, "znepg", znepgName+"-"+zone)
						return []*compute.Backend{}, err
					} else {
						needsCreation = true
					}
				}
			} else {
				if !strings.Contains(znepg.Network, network) || !strings.Contains(znepg.Subnetwork, subnetwork) || !strings.Contains(znepg.Zone, zone) || znepg.NetworkEndpointType != "GCE_VM_IP_PORT" {
					_, err := p.computeClient.NetworkEndpointGroups.Delete(p.googleProject, zone, znepgName).Context(context).Do()
					if err != nil {
						p.log.Error(err, "unable to delete zonal network endpoint group", "zone", zone, "znep", znepgName)
						return []*compute.Backend{}, err
					}
					needsCreation = true
				}
			}
			if needsCreation {
				op, err := p.computeClient.NetworkEndpointGroups.Insert(p.googleProject, zone, &compute.NetworkEndpointGroup{
					Zone:                zone,
					Network:             network,
					Subnetwork:          subnetwork,
					DefaultPort:         443,
					Name:                znepgName,
					NetworkEndpointType: "GCE_VM_IP_PORT",
				}).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create zonal network endpoint group", "zone", zone, "znep", znepgName)
					return []*compute.Backend{}, err
				}
				znepgID = op.TargetLink
			} else {
				znepgID = znepg.SelfLink
			}
			//at this point the znepg exists and it is correct, we can list its neps
			actualNeps := []compute.NetworkEndpoint{}
			err = p.computeClient.NetworkEndpointGroups.ListNetworkEndpoints(p.googleProject, zone, znepgName, &compute.NetworkEndpointGroupsListEndpointsRequest{}).Context(context).Pages(context, func(nepListResult *compute.NetworkEndpointGroupsListNetworkEndpoints) error {
				for _, neph := range nepListResult.Items {
					actualNeps = append(actualNeps, *neph.NetworkEndpoint)
				}
				return nil
			})
			if err != nil {
				p.log.Error(err, "unable to list network endpoints for zonal network endpoint group", "zone", zone, "znep", znepgName)
				return []*compute.Backend{}, err
			}
			//at this point we need to calculate which endpoit we need to delete vs which ones we need to add
			toBeDeleted, toBeCreated := p.getNepOuterJoin(actualNeps, desiredNeps)
			if len(toBeDeleted) > 0 {
				_, err = p.computeClient.NetworkEndpointGroups.DetachNetworkEndpoints(p.googleProject, zone, znepgName, &compute.NetworkEndpointGroupsDetachEndpointsRequest{
					NetworkEndpoints: toBeDeleted,
				}).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to delete", "endpoints", toBeDeleted, "znep", znepgName)
					return []*compute.Backend{}, err
				}
			}
			if len(toBeCreated) > 0 {
				_, err = p.computeClient.NetworkEndpointGroups.AttachNetworkEndpoints(p.googleProject, zone, znepgName, &compute.NetworkEndpointGroupsAttachEndpointsRequest{
					NetworkEndpoints: toBeCreated,
				}).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create", "endpoints", toBeDeleted, "znep", znepgName)
					return []*compute.Backend{}, err
				}
			}
			//at this point the znep is well configured, we can add it to the backends
			backends = append(backends, &compute.Backend{
				BalancingMode:             "CONNECTION",
				MaxConnectionsPerEndpoint: 100,
				Group:                     znepgID,
			})
		}
	}
	return backends, nil
}

func (p *googleGCPProvider) getNepOuterJoin(left []compute.NetworkEndpoint, right []compute.NetworkEndpoint) (leftOuterJoin []*compute.NetworkEndpoint, rightOuterJoin []*compute.NetworkEndpoint) {
	leftMap := map[string]*compute.NetworkEndpoint{}
	rightMap := map[string]*compute.NetworkEndpoint{}
	leftKeys := []string{}
	rightKeys := []string{}
	for _, ne := range left {
		ne1 := ne
		leftKeys = append(leftKeys, ne.Instance)
		leftMap[ne.Instance] = &ne1
	}
	for _, ne := range right {
		ne1 := ne
		rightKeys = append(rightKeys, ne.Instance)
		rightMap[ne.Instance] = &ne1
	}
	leftOuterJoinKeys := strset.Difference(strset.New(leftKeys...), strset.New(rightKeys...)).List()
	rightOuterJoinKeys := strset.Difference(strset.New(rightKeys...), strset.New(leftKeys...)).List()
	for _, lkey := range leftOuterJoinKeys {
		leftOuterJoin = append(leftOuterJoin, leftMap[lkey])
	}
	for _, rkey := range rightOuterJoinKeys {
		rightOuterJoin = append(rightOuterJoin, rightMap[rkey])
	}
	return
}

func (p *googleGCPProvider) compareHealthCheck(a, b *compute.HealthCheck) bool {
	if a.HttpHealthCheck != nil && b.HttpHealthCheck != nil && a.HttpHealthCheck.Host == b.HttpHealthCheck.Host && a.HttpHealthCheck.RequestPath == b.HttpHealthCheck.RequestPath && a.HttpHealthCheck.PortSpecification == b.HttpHealthCheck.PortSpecification {
		return false
	}
	if a.HttpsHealthCheck != nil && b.HttpsHealthCheck != nil && a.HttpsHealthCheck.Host == b.HttpsHealthCheck.Host && a.HttpsHealthCheck.RequestPath == b.HttpsHealthCheck.RequestPath && a.HttpsHealthCheck.PortSpecification == b.HttpsHealthCheck.PortSpecification {
		return false
	}
	return a.CheckIntervalSec == b.CheckIntervalSec &&
		a.UnhealthyThreshold == b.UnhealthyThreshold &&
		a.TimeoutSec == b.TimeoutSec &&
		a.Type == b.Type
}

func (p *googleGCPProvider) getDesiredHealthCheck() (*compute.HealthCheck, error) {
	var desiredHealthCheck *compute.HealthCheck
	switch p.instance.Spec.HealthCheck.HTTPGet.Scheme {
	case "HTTPS":
		desiredHealthCheck = &compute.HealthCheck{
			Name:               p.getHealthCheckName(),
			CheckIntervalSec:   int64(p.instance.Spec.HealthCheck.PeriodSeconds),
			UnhealthyThreshold: int64(p.instance.Spec.HealthCheck.FailureThreshold),
			TimeoutSec:         int64(p.instance.Spec.HealthCheck.TimeoutSeconds),
			Type:               "HTTPS",
			HttpsHealthCheck: &compute.HTTPSHealthCheck{
				Host:              p.instance.Spec.Name,
				RequestPath:       p.instance.Spec.HealthCheck.HTTPGet.Path,
				PortSpecification: "USE_SERVING_PORT",
			},
		}
	case "HTTP":
		desiredHealthCheck = &compute.HealthCheck{
			Name:               p.getHealthCheckName(),
			CheckIntervalSec:   int64(p.instance.Spec.HealthCheck.PeriodSeconds),
			UnhealthyThreshold: int64(p.instance.Spec.HealthCheck.FailureThreshold),
			TimeoutSec:         int64(p.instance.Spec.HealthCheck.TimeoutSeconds),
			Type:               "HTTP",
			HttpHealthCheck: &compute.HTTPHealthCheck{
				Host:              p.instance.Spec.Name,
				RequestPath:       p.instance.Spec.HealthCheck.HTTPGet.Path,
				PortSpecification: "USE_SERVING_PORT",
			},
		}
	default:
		return &compute.HealthCheck{}, errors.New("unsupported healthcheck scheme")
	}
	return desiredHealthCheck, nil
}

func (p *googleGCPProvider) ensureHealthCheck(context context.Context) (string, error) {
	desiredHealthCheck, err := p.getDesiredHealthCheck()
	if err != nil {
		p.log.Error(err, "unable to figure out desired healthcheck")
		return "", err
	}
	currentHealthCheck, err := p.computeClient.HealthChecks.Get(p.googleProject, p.getHealthCheckName()).Context(context).Do()
	if err != nil {
		if e, ok := err.(*googleapi.Error); ok {
			if e.Code != 404 {
				p.log.Error(err, "unable to get", "healthcheck", p.getHealthCheckName())
				return "", err
			} else {
				op, err := p.computeClient.HealthChecks.Insert(p.googleProject, desiredHealthCheck).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create healthcheck")
					return "", err
				}
				return op.TargetLink, nil
			}
		}
	}
	//update behaves like crete or update for name-assignable resources
	if !p.compareHealthCheck(currentHealthCheck, desiredHealthCheck) {
		op, err := p.computeClient.HealthChecks.Update(p.googleProject, p.getHealthCheckName(), desiredHealthCheck).Context(context).Do()
		if err != nil {
			p.log.Error(err, "unable to update healthcheck")
			return "", err
		}
		return op.TargetLink, nil
	}
	return currentHealthCheck.SelfLink, nil
}

func (p *googleGCPProvider) getDesiredFirewallRulesAllowForHealthChecks(endpoint *EndpointStatus) *compute.Firewall {
	return &compute.Firewall{
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
			},
		},
		Direction:    "INGRESS",
		SourceRanges: []string{"35.191.0.0/16", "130.211.0.0/22"},
		TargetTags:   []string{endpoint.infrastructure.Status.InfrastructureName + "-worker"},
		Network:      "projects/" + p.googleProject + "/global/networks/" + endpoint.infrastructure.Status.InfrastructureName + "-network",
		Name:         p.getFirewallRuleName(endpoint),
	}
}

func (p *googleGCPProvider) ensureFirewallRulesAllowForHealthChecks(context context.Context) error {
	for _, endpoint := range p.endpointMap {
		//update behaves like crete or update for name-assignable resources
		desiredFirewallRule := p.getDesiredFirewallRulesAllowForHealthChecks(&endpoint)
		currentFirewallRule, err := p.computeClient.Firewalls.Get(p.googleProject, p.getFirewallRuleName(&endpoint)).Context(context).Do()
		if err != nil {
			if e, ok := err.(*googleapi.Error); ok {
				if e.Code != 404 {
					p.log.Error(err, "unable to get", "firewall rule", p.getFirewallRuleName(&endpoint))
					return err
				} else {
					result, err := p.computeClient.Firewalls.Insert(p.googleProject, desiredFirewallRule).Context(context).Do()
					if err != nil {
						p.log.Error(err, "unable to create firewall rule to allow for healthchecks", "result", result)
						return err
					}
					continue
				}
			}
		}
		if !p.compareFirewallRules(desiredFirewallRule, currentFirewallRule) {
			result, err := p.computeClient.Firewalls.Update(p.googleProject, p.getFirewallRuleName(&endpoint), desiredFirewallRule).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to create/update firewall rule to allow for healthchecks", "result", result)
				return err
			}
		}
	}
	return nil
}

func (p *googleGCPProvider) compareFirewallRules(a, b *compute.Firewall) bool {
	if !(a.Allowed != nil && b.Allowed != nil && a.Allowed[0].IPProtocol == b.Allowed[0].IPProtocol) {
		return false
	}
	return a.Direction == b.Direction &&
		reflect.DeepEqual(a.SourceRanges, b.SourceRanges) &&
		reflect.DeepEqual(a.TargetTags, b.TargetTags) &&
		a.Network == b.Network &&
		a.Name == b.Name
}

func (p *googleGCPProvider) getDomain() string {
	return strings.Split(p.instance.Spec.Name, ".")[0]
}

func (p *googleGCPProvider) getRecordSetName() string {
	return p.instance.Spec.Name + "."
}

func (p *googleGCPProvider) getFirewallRuleName(endpoint *EndpointStatus) string {
	return endpoint.infrastructure.Status.InfrastructureName + "-fw-allow-health-checks"
}

func (p *googleGCPProvider) getHealthCheckName() string {
	return p.getDomain() + "-health-check"
}

func (p *googleGCPProvider) getBackendServiceName() string {
	return p.getDomain() + "-backend-service"
}

func (p *googleGCPProvider) getGlobalIPName() string {
	return p.getDomain() + "-global-ipv4"
}

func (p *googleGCPProvider) getTargetTCPProxyName() string {
	return p.getDomain() + "-global-ipv4"
}

func (p *googleGCPProvider) getForwardingRuleName() string {
	return p.getDomain() + "-forwarding-rule"
}
func (p *googleGCPProvider) getZonalNetworkEndpointGroupName(zone string) string {
	return p.getDomain() + "-" + zone
}

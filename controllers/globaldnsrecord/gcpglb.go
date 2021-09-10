package globaldnsrecord

import (
	"context"
	"errors"
	"strings"

	"github.com/go-logr/logr"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	cgoogle "github.com/redhat-cop/global-load-balancer-operator/controllers/common/google"
	"github.com/scylladb/go-set/strset"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
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

	if instance.Spec.HealthCheck != nil {
		return &googleGCPProvider{}, errors.New("missing healthcheck definition, heathchecks are mandatory in google")
	}

	computeClient, dnsClient, err := cgoogle.GetGCPClients(context, globalzone, &r.ReconcilerBase)
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
	op, err := p.computeClient.GlobalForwardingRules.Delete(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-forwarding-rule").Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "forwarding rule", p.getDomain(p.instance.Spec.Name)+"-forwarding-rule")
		return err
	}

	// delete global ip
	op, err = p.computeClient.GlobalAddresses.Delete(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-global-ipv4").Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getDomain(p.instance.Spec.Name)+"-global-ipv4")
		return err
	}

	// delete target tcp proxy
	op, err = p.computeClient.TargetTcpProxies.Delete(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-target-tcp-proxy").Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getDomain(p.instance.Spec.Name)+"-target-tcp-proxy")
		return err
	}

	// delete backend service
	op, err = p.computeClient.BackendServices.Delete(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-backend-service").Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getDomain(p.instance.Spec.Name)+"-backend-service")
		return err
	}

	// delete health check
	op, err = p.computeClient.HealthChecks.Delete(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-health-check").Context(context).Do()
	if err != nil && op.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to delete", "global address", p.getDomain(p.instance.Spec.Name)+"-health-check")
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
			op, err = p.computeClient.NetworkEndpointGroups.Delete(p.googleProject, zone, p.getDomain(p.instance.Spec.Name)+"-"+zone).Context(context).Do()
			if err != nil && op.HTTPStatusCode != 404 {
				p.log.Error(err, "unable to delete", "global address", p.getDomain(p.instance.Spec.Name)+"-"+zone)
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

func (p *googleGCPProvider) ensureDNSEntry(context context.Context, globalIP string) error {
	rrs, err := p.dnsClient.ResourceRecordSets.Get(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, p.instance.Spec.Name, "A").Context(context).Do()
	if err != nil && rrs.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to get", "dns record", p.instance.Spec.Name)
		return err
	}
	if rrs.HTTPStatusCode == 404 {
		_, err := p.dnsClient.ResourceRecordSets.Create(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, &dns.ResourceRecordSet{
			Name:    p.instance.Spec.Name,
			Type:    "A",
			Ttl:     int64(p.instance.Spec.TTL),
			Rrdatas: []string{globalIP},
		}).Context(context).Do()
		if err != nil {
			p.log.Error(err, "unable to create", "dns entry", p.instance.Spec.Name, "ip", globalIP)
			return err
		}
	} else {
		if rrs.Ttl != int64(p.instance.Spec.TTL) || rrs.Rrdatas[0] != globalIP {
			_, err = p.dnsClient.ResourceRecordSets.Patch(p.googleProject, p.globalzone.Spec.Provider.GCPGLB.ManagedZoneName, "A", p.instance.Spec.Name, &dns.ResourceRecordSet{
				Name:    p.instance.Spec.Name,
				Type:    "A",
				Ttl:     int64(p.instance.Spec.TTL),
				Rrdatas: []string{globalIP},
			}).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to update", "dns entry", p.instance.Spec.Name, "ip", globalIP)
				return err
			}
		}
	}
	return nil
}

func (p *googleGCPProvider) ensureForwardingRule(context context.Context, targetTCPProxyID string, globalIP string) error {
	name := p.getDomain(p.instance.Spec.Name) + "-forwarding-rule"
	var port string
	forwardingRule, err := p.computeClient.GlobalForwardingRules.Get(p.googleProject, name).Context(context).Do()
	if err != nil && forwardingRule.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to get", "forwardingRule", name)
		return err
	}
	if forwardingRule.HTTPStatusCode == 404 {
		_, err := p.computeClient.GlobalForwardingRules.Insert(p.googleProject, &compute.ForwardingRule{
			IPProtocol:          "TCP",
			IPAddress:           globalIP,
			LoadBalancingScheme: "EXTERNAL",
			Ports:               []string{port},
			Target:              targetTCPProxyID,
		}).Context(context).Do()
		if err != nil {
			p.log.Error(err, "unable to create global forwarding rule")
			return err
		}
	} else {
		if forwardingRule.Target != targetTCPProxyID {
			_, err := p.computeClient.GlobalForwardingRules.SetTarget(p.googleProject, name, &compute.TargetReference{
				Target: targetTCPProxyID,
			}).Context(context).Do()
			if err != nil {
				p.log.Error(err, "unable to set target on", "forwarding rule", name, "targetTCPProxyID", targetTCPProxyID)
				return err
			}
		}
	}
	return nil
}

func (p *googleGCPProvider) ensureTargetTCPProxy(context context.Context, backendID string) (string, error) {
	name := p.getDomain(p.instance.Spec.Name) + "-target-tcp-proxy"
	var targetTCPProxyID string
	targetTCPProxy, err := p.computeClient.TargetTcpProxies.Get(p.googleProject, name).Context(context).Do()
	if err != nil && targetTCPProxy.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to get", "targetTCPProxy", name)
		return "", err
	}
	if targetTCPProxy.HTTPStatusCode == 404 {
		op, err := p.computeClient.TargetTcpProxies.Insert(p.googleProject, &compute.TargetTcpProxy{
			Name:    name,
			Service: backendID,
		}).Context(context).Do()
		if err != nil {
			p.log.Error(err, "unable to create global address")
			return "", err
		}
		targetTCPProxyID = op.SelfLink

	} else {
		if targetTCPProxy.Service != backendID {
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
	name := p.getDomain(p.instance.Spec.Name) + "-global-ipv4"
	var IP string
	address, err := p.computeClient.GlobalAddresses.Get(p.googleProject, name).Context(context).Do()
	if err != nil && address.HTTPStatusCode != 404 {
		p.log.Error(err, "unable to get", "address", name)
		return "", err
	}
	if address.HTTPStatusCode == 404 {
		//we have to create the znep
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
		if err != nil && address.HTTPStatusCode != 404 {
			p.log.Error(err, "unable to get", "address", name)
			return "", err
		}
		IP = address.Address
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
		Name:      endpoint.endpoint.LoadBalancerServiceRef.Namespace,
	}, service)
	if err != nil {
		p.log.Error(err, "unable to get", "service", types.NamespacedName{
			Namespace: endpoint.endpoint.LoadBalancerServiceRef.Namespace,
			Name:      endpoint.endpoint.LoadBalancerServiceRef.Namespace,
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
		network := endpoint.infrastructure.Status.InfrastructureName + "-network"
		subnetwork := endpoint.infrastructure.Status.InfrastructureName + "-worker-subnet"
		desiredNepsByZone, err := p.getDesiredNepsByZone(context, &endpoint)
		if err != nil {
			p.log.Error(err, "unable to get selected nodes by zone for", "endpoint", name)
			return []*compute.Backend{}, err
		}
		for zone, desiredNeps := range desiredNepsByZone {
			// verify if network endpoint group already exists
			var znepgID string
			znepgName := p.getDomain(p.instance.Spec.Name) + "-" + zone
			znepg, err := p.computeClient.NetworkEndpointGroups.Get(p.googleProject, zone, znepgName).Context(context).Do()
			if err != nil && znepg.HTTPStatusCode != 404 {
				p.log.Error(err, "unable to get zonal network endpoint group", "zone", zone, "znepg", znepgName+"-"+zone)
				return []*compute.Backend{}, err
			}
			if znepg.HTTPStatusCode == 404 {
				//we have to create the znep
				op, err := p.computeClient.NetworkEndpointGroups.Insert(p.googleProject, zone, &compute.NetworkEndpointGroup{
					Zone:        zone,
					Network:     network,
					Subnetwork:  subnetwork,
					DefaultPort: 443,
					Name:        znepgName,
				}).Context(context).Do()
				if err != nil {
					p.log.Error(err, "unable to create zonal network endpoint group", "zone", zone, "znep", znepgName)
					return []*compute.Backend{}, err
				}
				znepgID = op.SelfLink
			} else {
				// the znep exists, we need to check it has the right values
				if znepg.Network != network || znepg.Subnetwork != subnetwork {
					//we have do delete it and recreate it
					_, err := p.computeClient.NetworkEndpointGroups.Delete(p.googleProject, zone, znepgName).Context(context).Do()
					if err != nil {
						p.log.Error(err, "unable to delet zonal network endpoint group", "zone", zone, "znep", znepgName)
						return []*compute.Backend{}, err
					}
					op, err := p.computeClient.NetworkEndpointGroups.Insert(p.googleProject, zone, &compute.NetworkEndpointGroup{
						Zone:        zone,
						Network:     network,
						Subnetwork:  subnetwork,
						DefaultPort: 443,
						Name:        znepgName,
					}).Context(context).Do()
					if err != nil {
						p.log.Error(err, "unable to create zonal endpoint group", "zone", zone, "znep", znepgName)
						return []*compute.Backend{}, err
					}
					znepgID = op.SelfLink
				} else {
					znepgID = znepg.SelfLink
				}
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
		leftKeys = append(leftKeys, ne.Instance)
		leftMap[ne.Instance] = &ne
	}
	for _, ne := range right {
		rightKeys = append(rightKeys, ne.Instance)
		rightMap[ne.Instance] = &ne
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

func (p *googleGCPProvider) ensureBackendService(context context.Context, healthCheckID string, znegs []*compute.Backend) (string, error) {
	op, err := p.computeClient.BackendServices.Update(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-backend-service", &compute.BackendService{
		Protocol:            "TCP",
		LoadBalancingScheme: "EXTERNAL",
		HealthChecks:        []string{healthCheckID},
		Backends:            znegs,
	}).Context(context).Do()
	if err != nil {
		p.log.Error(err, "unable to create/update healthcheck")
		return "", err
	}
	return op.SelfLink, nil
}

func (p *googleGCPProvider) ensureHealthCheck(context context.Context) (string, error) {
	var healthCheck *compute.HealthCheck
	switch p.instance.Spec.HealthCheck.HTTPGet.Scheme {
	case "HTTPS":
		healthCheck = &compute.HealthCheck{
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
		healthCheck = &compute.HealthCheck{
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
		return "", errors.New("unsupported healthcheck scheme")
	}
	//update behaves like crete or update for name-assignable resources
	op, err := p.computeClient.HealthChecks.Update(p.googleProject, p.getDomain(p.instance.Spec.Name)+"-health-check", healthCheck).Context(context).Do()
	if err != nil {
		p.log.Error(err, "unable to create/update healthcheck")
		return "", err
	}
	return op.SelfLink, nil
}

func (p *googleGCPProvider) ensureFirewallRulesAllowForHealthChecks(context context.Context) error {
	for _, endpoint := range p.endpointMap {
		//update behaves like crete or update for name-assignable resources
		_, err := p.computeClient.Firewalls.Update(p.googleProject, endpoint.infrastructure.Status.InfrastructureName+"-fw-allow-health-checks", &compute.Firewall{
			Allowed: []*compute.FirewallAllowed{
				{
					IPProtocol: "tcp",
				},
			},
			Direction:    "INGRESS",
			SourceRanges: []string{"35.191.0.0/16", "130.211.0.0/22"},
			TargetTags:   []string{p.googleProject, endpoint.infrastructure.Status.InfrastructureName + "-worker"},
			Network:      endpoint.infrastructure.Status.InfrastructureName + "-network",
		}).Context(context).Do()
		if err != nil {
			p.log.Error(err, "unable to create/update firewall rule to allow for healthchecks")
			return err
		}
	}
	return nil
}

func (p *googleGCPProvider) getDomain(fqdn string) string {
	return strings.Split(fqdn, ".")[0]
}

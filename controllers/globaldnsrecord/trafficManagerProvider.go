package globaldnsrecord

import (
	"context"
	"errors"
	"math/rand"
	"strings"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/dns/mgmt/dns"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/trafficmanager/mgmt/trafficmanager"

	"github.com/go-logr/logr"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	cazure "github.com/redhat-cop/global-load-balancer-operator/controllers/common/azure"
)

type trafficManagerProvider struct {
	instance     *redhatcopv1alpha1.GlobalDNSRecord
	globalzone   *redhatcopv1alpha1.GlobalDNSZone
	endpointMap  map[string]EndpointStatus
	log          logr.Logger
	azureClients *cazure.AzureClients
}

var _ globalDNSProvider = &trafficManagerProvider{}

func (r *GlobalDNSRecordReconciler) createTrafficManagerProvider(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (*trafficManagerProvider, error) {
	// validate that all infratsrcutre is homogeneus
	for _, endpointStatus := range endpointMap {
		if endpointStatus.infrastructure.Status.PlatformStatus.Type != ocpconfigv1.AzurePlatformType {
			err := errors.New("Illegal state, only Azure endpoints are allowed")
			r.Log.Error(err, "need azrue endpoint", "endpoint type", endpointStatus.infrastructure.Status.PlatformStatus.Type)
			return &trafficManagerProvider{}, err
		}
	}

	azureClients, err := cazure.GetAzureClient(context, globalzone, &r.ReconcilerBase)
	if err != nil {
		r.Log.Error(err, "unable to get trafficManagerClient client")
		return &trafficManagerProvider{}, err
	}
	return &trafficManagerProvider{
		instance:     instance,
		globalzone:   globalzone,
		endpointMap:  endpointMap,
		azureClients: azureClients,
		log:          r.Log.WithName("trafficManagerprovider"),
	}, nil
}

func (p *trafficManagerProvider) deleteDNSRecord(context context.Context) error {
	//  delete traffic manager profile
	_, err := p.azureClients.GetProfilesClient().Delete(context, p.globalzone.Spec.Provider.TrafficManager.ResourceGroup, p.getDomain(p.instance.Spec.Name))

	if err != nil {
		p.log.Error(err, "unable to delete", "traffic manager", p.getDomain(p.instance.Spec.Name))
		return err
	}

	// delete dns entry
	_, err = p.azureClients.GetDNSClient().Delete(context, p.globalzone.Spec.Provider.TrafficManager.DNSZoneResourceGroup, p.globalzone.Spec.Domain, p.getDomain(p.instance.Spec.Name), dns.CNAME, "")
	if err != nil {
		p.log.Error(err, "unable to delete", "dns entry", p.instance.Spec.Name)
		return err
	}
	return nil
}

func (p *trafficManagerProvider) ensureDNSRecord(context context.Context) error {

	//  ensure public IPS have fqdn
	clusterPublicIPMap, err := p.ensurePublicIPsHaveFQDNs(context, p.endpointMap)
	if err != nil {
		p.log.Error(err, "unable to ensure publicIPs have FQDNs")
		return err
	}

	// ensure traffic manager profile
	trafficManager, err := p.ensureTrafficManagerProfile(context, clusterPublicIPMap)
	if err != nil {
		p.log.Error(err, "unable to create/update traffic manager profile")
		return err
	}

	// create dns entry

	err = p.ensureAzureDNSRecord(context, trafficManager)
	if err != nil {
		p.log.Error(err, "unable to create/update dns record")
		return err
	}

	return nil
}

func (p *trafficManagerProvider) ensureAzureDNSRecord(context context.Context, tmProfile *trafficmanager.Profile) error {
	ttl := int64(p.instance.Spec.TTL)
	_, err := p.azureClients.GetDNSClient().CreateOrUpdate(context, p.globalzone.Spec.Provider.TrafficManager.DNSZoneResourceGroup, p.globalzone.Spec.Domain, p.getDomain(p.instance.Spec.Name), dns.CNAME, dns.RecordSet{
		RecordSetProperties: &dns.RecordSetProperties{
			TTL:  &ttl,
			Fqdn: &p.instance.Spec.Name,
			CnameRecord: &dns.CnameRecord{
				Cname: tmProfile.ProfileProperties.DNSConfig.Fqdn,
			},
		}}, "", "")
	if err != nil {
		p.log.Error(err, "unable to create/update dns record")
		return err
	}
	return nil
}

func (p *trafficManagerProvider) ensureTrafficManagerProfile(context context.Context, clusterPublicIPMap map[string]network.PublicIPAddress) (*trafficmanager.Profile, error) {
	if p.instance.Status.ProviderStatus.TrafficManager == nil || p.instance.Status.ProviderStatus.TrafficManager.Name == "" {
		potentialName := p.getDomain(p.instance.Spec.Name) + "-" + p.randDomain(10)
		result, err := p.azureClients.GetProfilesClient().CheckTrafficManagerRelativeDNSNameAvailability(context, trafficmanager.CheckTrafficManagerRelativeDNSNameAvailabilityParameters{
			Name: &potentialName,
		})
		if err != nil {
			p.log.Error(err, "unable to check dns availability for", "potentialName", potentialName)
			return &trafficmanager.Profile{}, err
		}
		if !*result.NameAvailable {
			return &trafficmanager.Profile{}, errors.New("dns name not available will retry at the next reconciliation cycle")
		}
		p.instance.Status.ProviderStatus.TrafficManager = &redhatcopv1alpha1.TrafficManagerProviderStatus{
			Name: potentialName,
		}
	}
	ttl := int64(p.instance.Spec.TTL)
	routingMethod, err := p.getTrafficRoutingMethod()
	zero := int64(0)
	global := "global"
	if err != nil {
		p.log.Error(err, "unsupported", "routing method", p.instance.Spec.LoadBalancingPolicy)
		return &trafficmanager.Profile{}, err
	}
	var monitoringConfig trafficmanager.MonitorConfig
	withHealthChecks := false
	if p.instance.Spec.HealthCheck != nil {
		port := int64(p.instance.Spec.HealthCheck.HTTPGet.Port.IntVal)
		//always slow probing for now
		intervalInSeconds := int64(30)
		timeoutInSeconds := p.min(p.max(int64(p.instance.Spec.HealthCheck.TimeoutSeconds), 5), 10)
		p.log.V(1).Info("", "intervalInSeconds", intervalInSeconds, "timeoutInSeconds", timeoutInSeconds)
		toleratedNumberOfFailures := int64(p.instance.Spec.HealthCheck.FailureThreshold)
		host := "host"
		monitoringConfig = trafficmanager.MonitorConfig{
			ProfileMonitorStatus:      trafficmanager.ProfileMonitorStatusCheckingEndpoints,
			Protocol:                  trafficmanager.MonitorProtocol(p.instance.Spec.HealthCheck.HTTPGet.Scheme),
			Port:                      &port,
			Path:                      &p.instance.Spec.HealthCheck.HTTPGet.Path,
			IntervalInSeconds:         &intervalInSeconds,
			TimeoutInSeconds:          &timeoutInSeconds,
			ToleratedNumberOfFailures: &toleratedNumberOfFailures,
			CustomHeaders: &[]trafficmanager.MonitorConfigCustomHeadersItem{
				{
					Name:  &host,
					Value: &p.instance.Spec.Name,
				},
			},
		}
		withHealthChecks = true
	} else {
		//looks like we have to make something up
		fft := int64(443)
		path := "/path"
		monitoringConfig = trafficmanager.MonitorConfig{
			ProfileMonitorStatus: trafficmanager.ProfileMonitorStatusInactive,
			Protocol:             trafficmanager.MonitorProtocol("https"),
			Port:                 &fft,
			Path:                 &path,
		}
	}
	endpoints, err := p.getEndpoints(clusterPublicIPMap, withHealthChecks)
	if err != nil {
		p.log.Error(err, "unable to get endpoints for traffic manager", "clusterPublicIPMap", clusterPublicIPMap)
		return &trafficmanager.Profile{}, err
	}
	trafficManagerProfile := trafficmanager.Profile{
		Location: &global,
		ProfileProperties: &trafficmanager.ProfileProperties{
			ProfileStatus:        trafficmanager.ProfileStatusEnabled,
			TrafficRoutingMethod: routingMethod,
			DNSConfig: &trafficmanager.DNSConfig{
				RelativeName: &p.instance.Status.ProviderStatus.TrafficManager.Name,
				Fqdn:         &p.instance.Spec.Name,
				TTL:          &ttl,
			},
			MonitorConfig:               &monitoringConfig,
			Endpoints:                   endpoints,
			TrafficViewEnrollmentStatus: trafficmanager.TrafficViewEnrollmentStatusDisabled,
			MaxReturn:                   &zero,
		},
	}
	tmProfile, err := p.azureClients.GetProfilesClient().CreateOrUpdate(context, p.globalzone.Spec.Provider.TrafficManager.ResourceGroup, p.instance.Status.ProviderStatus.TrafficManager.Name, trafficManagerProfile)
	if err != nil {
		p.log.Error(err, "unable to create or update traffic manager profile", "name", p.instance.Status.ProviderStatus.TrafficManager.Name, "trafficManagerProfile", trafficManagerProfile)
		return &trafficmanager.Profile{}, err
	}
	p.log.Info("created", "trafficManagerProfile", tmProfile)
	return &tmProfile, nil
}

func (p *trafficManagerProvider) getTrafficRoutingMethod() (trafficmanager.TrafficRoutingMethod, error) {
	switch p.instance.Spec.LoadBalancingPolicy {
	case redhatcopv1alpha1.Multivalue:
		return trafficmanager.TrafficRoutingMethodMultiValue, nil
	// case redhatcopv1alpha1.Weighted:
	// 	return trafficmanager.TrafficRoutingMethodWeighted, nil
	case redhatcopv1alpha1.Geographic:
		return trafficmanager.TrafficRoutingMethodGeographic, nil
	case redhatcopv1alpha1.Performance:
		return trafficmanager.TrafficRoutingMethodPerformance, nil
	case redhatcopv1alpha1.Latency:
		return trafficmanager.TrafficRoutingMethodPerformance, nil
	case redhatcopv1alpha1.Geoproximity:
		return trafficmanager.TrafficRoutingMethodGeographic, nil
	default:
		return "", errors.New("unsupported routing method")
	}
}

func (p *trafficManagerProvider) getEndpoints(clusterPublicIPMap map[string]network.PublicIPAddress, withHealthChecks bool) (*[]trafficmanager.Endpoint, error) {
	tmEndpoints := []trafficmanager.Endpoint{}
	one := int64(1)
	_ = "Microsoft.Network/trafficManagerProfiles/externalEndpoints"
	azureEndpoint := "Microsoft.Network/trafficManagerProfiles/azureEndpoints"
	var monitoringStatus trafficmanager.EndpointMonitorStatus
	if withHealthChecks {
		monitoringStatus = trafficmanager.EndpointMonitorStatusCheckingEndpoint
	} else {
		monitoringStatus = trafficmanager.EndpointMonitorStatusInactive
	}
	for cluster, endpoint := range p.endpointMap {
		endPointName := p.getEndpointName(endpoint)
		tmEndpoints = append(tmEndpoints, trafficmanager.Endpoint{
			Name: &endPointName,
			Type: &azureEndpoint,
			EndpointProperties: &trafficmanager.EndpointProperties{
				EndpointStatus:        trafficmanager.EndpointStatusEnabled,
				EndpointMonitorStatus: monitoringStatus,
				TargetResourceID:      clusterPublicIPMap[cluster].ID,
				Target:                clusterPublicIPMap[cluster].DNSSettings.Fqdn,
				Weight:                &one,
				//Priority:              &one,
				EndpointLocation: clusterPublicIPMap[cluster].Location,
			},
		})
	}
	return &tmEndpoints, nil
}

func (p *trafficManagerProvider) getEndpointName(endpoint EndpointStatus) string {
	return endpoint.endpoint.ClusterName
}
func (p *trafficManagerProvider) ensurePublicIPsHaveFQDNs(context context.Context, endpoints map[string]EndpointStatus) (map[string]network.PublicIPAddress, error) {
	clusterPublicIPMap := map[string]network.PublicIPAddress{}
	for cluster, endpoint := range endpoints {
		p.log.V(1).Info("examining public IP for", "cluster", cluster)
		found := false
		IPs, err := endpoint.getIPs()
		if err != nil {
			p.log.Error(err, "unable to get IPs from", "endpoint", endpoint)
			return map[string]network.PublicIPAddress{}, err
		}
		if len(IPs) != 1 {
			return map[string]network.PublicIPAddress{}, errors.New("illegal state: when running on azure, load balancer should have only one IP")
		}
		publicIPsResponse, err := p.azureClients.GetPublicIPAddressesClient().ListComplete(context, endpoint.infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName)
		if err != nil {
			p.log.Error(err, "unable to get the list of public IPs")
			return map[string]network.PublicIPAddress{}, err
		}
		p.log.V(1).Info("", "publicIPsResponse", publicIPsResponse)
		publicIPs := []network.PublicIPAddress{}
		for publicIPsResponse.NotDone() {
			publicIP := publicIPsResponse.Value()
			err := publicIPsResponse.NextWithContext(context)
			publicIPs = append(publicIPs, publicIP)
			if err != nil {
				p.log.Error(err, "unable to get next IPs from", "iterator", publicIPsResponse)
				return map[string]network.PublicIPAddress{}, err
			}
		}
		p.log.V(1).Info("", "publicIPs", publicIPs)
		for _, publicIP := range publicIPs {
			if *publicIP.PublicIPAddressPropertiesFormat.IPAddress == IPs[0] {
				found = true
				p.log.Info("found", "publicIP", publicIP, " for cluster", cluster)
				if publicIP.PublicIPAddressPropertiesFormat.DNSSettings == nil || publicIP.PublicIPAddressPropertiesFormat.DNSSettings.Fqdn == nil {
					//generate uuid
					domain := p.randDomain(10)
					//check for dns availability
					result, err := p.azureClients.GetPublicIPAddressesClient().CheckDNSNameAvailability(context, *publicIP.Location, domain)
					if err != nil {
						p.log.Error(err, "unable to get availability status for fqdn", "publicIP", publicIP, "domain", domain)
						return map[string]network.PublicIPAddress{}, err
					}
					if !*result.Available {
						err := errors.New("domain not available")
						p.log.Error(err, "unavailable fqdn", "publicIP", publicIP, "domain", domain, "result", result)
						return map[string]network.PublicIPAddress{}, err
					}
					publicIP.DNSSettings = &network.PublicIPAddressDNSSettings{
						DomainNameLabel: &domain,
					}
					//update IP with fqdn
					_, err = p.azureClients.GetPublicIPAddressesClient().CreateOrUpdate(context, endpoint.infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName, *publicIP.Name, publicIP)
					if err != nil {
						p.log.Error(err, "unable to update", "publicIP", publicIP, "with domain", domain)
						return map[string]network.PublicIPAddress{}, err
					}
				}
				clusterPublicIPMap[cluster] = publicIP
				break
			}
		}
		if !found {
			err := errors.New("loadbalancer public ip not found in azure public IPs")
			p.log.Error(err, "load balancer", "ip", IPs[0], "cluster", cluster)
			return map[string]network.PublicIPAddress{}, err
		}
	}
	p.log.Info("", "clusterPublicIPMap", clusterPublicIPMap)
	return clusterPublicIPMap, nil
}

func (p *trafficManagerProvider) randDomain(length int) string {
	charset := "qwertyuioplkjhgfdsazxcvbnm"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func (p *trafficManagerProvider) getDomain(fqdn string) string {
	return strings.Split(fqdn, ".")[0]
}

// Max returns the larger of x or y.
func (p *trafficManagerProvider) max(x, y int64) int64 {
	if x < y {
		return y
	}
	return x
}

// Min returns the smaller of x or y.
func (p *trafficManagerProvider) min(x, y int64) int64 {
	if x > y {
		return y
	}
	return x
}

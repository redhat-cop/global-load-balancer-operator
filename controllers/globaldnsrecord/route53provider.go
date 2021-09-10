package globaldnsrecord

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/go-logr/logr"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/api/v1alpha1"
	tpdroute53 "github.com/redhat-cop/global-load-balancer-operator/controllers/common/route53"
	"github.com/redhat-cop/global-load-balancer-operator/controllers/common/route53/route53endpointrulereferenceset"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

type route53Provider struct {
	instance      *redhatcopv1alpha1.GlobalDNSRecord
	globalzone    *redhatcopv1alpha1.GlobalDNSZone
	endpointMap   map[string]EndpointStatus
	log           logr.Logger
	route53Client *route53.Route53
}

var _ globalDNSProvider = &route53Provider{}

func (r *GlobalDNSRecordReconciler) createRoute53Provider(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (*route53Provider, error) {
	// validate that all infratsrcutre is homogeneus
	for _, endpointStatus := range endpointMap {
		if endpointStatus.infrastructure.Status.PlatformStatus.Type != ocpconfigv1.AWSPlatformType {
			err := errors.New("Illegal state, only AWS endpoints are allowed")
			r.Log.Error(err, "need aws endpoint", "endpoint type", endpointStatus.infrastructure.Status.PlatformStatus.Type)
			return &route53Provider{}, err
		}
	}

	route53Client, err := tpdroute53.GetRoute53Client(context, globalzone, &r.ReconcilerBase)
	if err != nil {
		r.Log.Error(err, "unable to get route53 client")
		return &route53Provider{}, err
	}
	return &route53Provider{
		instance:      instance,
		globalzone:    globalzone,
		endpointMap:   endpointMap,
		route53Client: route53Client,
		log:           r.Log.WithName("route53provider"),
	}, nil
}

func (p *route53Provider) deleteDNSRecord(context context.Context) error {
	return p.cleanUpRoute53DNSRecord(context)
}

func (p *route53Provider) ensureDNSRecord(context context.Context) error {

	if p.instance.Status.ProviderStatus.Route53 == nil {
		p.instance.Status.ProviderStatus.Route53 = &redhatcopv1alpha1.Route53ProviderStatus{}
	}

	// ensure traffic policy
	trafficPolicyID, err := p.ensureRoute53TrafficPolicy()
	if err != nil {
		p.log.Error(err, "unable to ensure the existance of", "traffic policy", p.instance.Spec.LoadBalancingPolicy)
		return err
	}
	p.instance.Status.ProviderStatus.Route53.PolicyID = trafficPolicyID
	// ensure dns record
	trafficPolicyInstanceID, err := p.ensureRoute53DNSRecord(trafficPolicyID)
	if err != nil {
		p.log.Error(err, "unable to ensure the existance of", "dns record", p.instance.Spec.Name)
		return err
	}
	p.instance.Status.ProviderStatus.Route53.PolicyInstanceID = trafficPolicyInstanceID
	return nil
}

// func (p *route53Provider) createRoute53Record(context context.Context, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (reconcile.Result, error) {

// 	// validate that all infratsrcutre is homogeneus
// 	for _, endpointStatus := range endpointMap {
// 		if endpointStatus.infrastructure.Status.PlatformStatus.Type != ocpconfigv1.AWSPlatformType {
// 			err := errors.New("Illegal state, only AWS endpoints are allowed")
// 			r.Log.Error(err, "need aws endpoint", "endpoint type", endpointStatus.infrastructure.Status.PlatformStatus.Type)
// 			return r.ManageError(context, instance, endpointMap, err)
// 		}
// 	}

// 	route53Client, err := tpdroute53.GetRoute53Client(context, globalzone, &r.ReconcilerBase)
// 	if err != nil {
// 		r.Log.Error(err, "unable to get route53 client")
// 		return r.ManageError(context, instance, endpointMap, err)
// 	}

// 	if instance.Status.ProviderStatus.Route53 == nil {
// 		instance.Status.ProviderStatus.Route53 = &redhatcopv1alpha1.Route53ProviderStatus{}
// 	}

// 	// ensure traffic policy
// 	trafficPolicyID, err := r.ensureRoute53TrafficPolicy(instance, route53Client, endpointMap)
// 	if err != nil {
// 		r.Log.Error(err, "unable to ensure the existance of", "traffic policy", instance.Spec.LoadBalancingPolicy)
// 		return r.ManageError(context, instance, endpointMap, err)
// 	}
// 	instance.Status.ProviderStatus.Route53.PolicyID = trafficPolicyID
// 	// ensure dns record
// 	trafficPolicyInstanceID, err := r.ensureRoute53DNSRecord(instance, globalzone, route53Client, trafficPolicyID)
// 	if err != nil {
// 		r.Log.Error(err, "unable to ensure the existance of", "dns record", instance.Spec.Name)
// 		return r.ManageError(context, instance, endpointMap, err)
// 	}
// 	instance.Status.ProviderStatus.Route53.PolicyInstanceID = trafficPolicyInstanceID
// 	return r.ManageSuccess(context, instance, endpointMap)
// }

func (p *route53Provider) ensureRoute53HealthCheck(ip string) (string, error) {

	healthChecks, err := p.route53Client.ListHealthChecks(&route53.ListHealthChecksInput{})
	if err != nil {
		p.log.Error(err, "unable to list route53 healthchecks")
		return "", err
	}
	var currentHealthCheck *route53.HealthCheck
	for _, healthCheck := range healthChecks.HealthChecks {
		if p.instance.Spec.HealthCheck.HTTPGet != nil && healthCheck.HealthCheckConfig.FullyQualifiedDomainName != nil && *healthCheck.HealthCheckConfig.FullyQualifiedDomainName == p.instance.Spec.HealthCheck.HTTPGet.Host && *healthCheck.HealthCheckConfig.IPAddress == ip {
			p.log.V(1).Info("found existing", "health check for", *healthCheck.HealthCheckConfig.FullyQualifiedDomainName)
			currentHealthCheck = healthCheck
		}
	}

	if currentHealthCheck == nil {
		//we need to create a new healthcheck
		healthCheckID, err := p.createHealthCheck(p.instance.Spec.HealthCheck, ip)
		if err != nil {
			p.log.Error(err, "unable to create route53 healthcheck", "for probe", p.instance.Spec.HealthCheck)
			return "", err
		}
		return healthCheckID, nil
	}

	//if we are here the helth check already exists, we need to check that it's still current
	newHealthCheckConfig, err := p.getAWSHealthCheckConfig(p.instance.Spec.HealthCheck, ip)
	if err != nil {
		p.log.Error(err, "unable to convert probe to aws health check", "probe", p.instance.Spec.HealthCheck)
		return "", err
	}
	p.log.V(1).Info("health check was found, checking if it needs update")
	if !reflect.DeepEqual(newHealthCheckConfig, currentHealthCheck.HealthCheckConfig) {
		// we need to update the health check
		p.log.V(1).Info("health check needs update, updating")
		updateHealthCheckInput := p.getAWSUpdateHealthCheckInput(newHealthCheckConfig)
		updateHealthCheckInput.HealthCheckId = currentHealthCheck.Id
		result, err := p.route53Client.UpdateHealthCheck(updateHealthCheckInput)
		if err != nil {
			p.log.Error(err, "unable to update aws health check", "probe", updateHealthCheckInput)
			return "", err
		}
		return *result.HealthCheck.Id, nil
	}
	p.log.V(1).Info("health does not need update")
	return *currentHealthCheck.Id, nil
}

func (p *route53Provider) createHealthCheck(probe *corev1.Probe, ip string) (string, error) {
	healthCheckConfig, err := p.getAWSHealthCheckConfig(probe, ip)
	if err != nil {
		p.log.Error(err, "unable to convert probe to aws health check", "probe", probe)
		return "", err
	}
	healthCheckInput := route53.CreateHealthCheckInput{
		CallerReference:   aws.String((string(uuid.NewUUID()) + "|" + *healthCheckConfig.IPAddress + "@" + *healthCheckConfig.FullyQualifiedDomainName)[:64]),
		HealthCheckConfig: healthCheckConfig,
	}
	result, err := p.route53Client.CreateHealthCheck(&healthCheckInput)
	if err != nil {
		p.log.Error(err, "unable to cretae AWS", "health check", healthCheckInput)
		return "", err
	}
	//add tagging of health check
	healthCheckID := strings.Split(*result.Location, "/")[len(strings.Split(*result.Location, "/"))-1]
	_, err = p.route53Client.ChangeTagsForResource(&route53.ChangeTagsForResourceInput{
		AddTags: []*route53.Tag{
			{
				Key:   aws.String("Name"),
				Value: aws.String(probe.HTTPGet.Host + "@" + ip),
			},
		},
		ResourceId:   aws.String(healthCheckID),
		ResourceType: aws.String("healthcheck"),
	})
	if err != nil {
		p.log.Error(err, "unable to tag", "health check", healthCheckID)
		return "", err
	}
	return healthCheckID, nil
}

func (p *route53Provider) getAWSHealthCheckConfig(probe *corev1.Probe, ip string) (*route53.HealthCheckConfig, error) {
	if probe == nil || probe.HTTPGet == nil {
		err := errors.New("invalid health check")
		p.log.Error(err, "healh check must be of http get type")
		return nil, err
	}
	return &route53.HealthCheckConfig{
		IPAddress:                aws.String(ip),
		Disabled:                 aws.Bool(false),
		Inverted:                 aws.Bool(false),
		MeasureLatency:           aws.Bool(false),
		EnableSNI:                aws.Bool(probe.HTTPGet.Scheme == corev1.URISchemeHTTPS),
		FailureThreshold:         aws.Int64(int64(probe.FailureThreshold)),
		FullyQualifiedDomainName: &probe.HTTPGet.Host,
		Port:                     aws.Int64(int64(probe.HTTPGet.Port.IntVal)),
		//HealthThreshold:          aws.Int64(int64(probe.SuccessThreshold)),
		RequestInterval: aws.Int64(int64(probe.PeriodSeconds)),
		ResourcePath:    &probe.HTTPGet.Path,
		Type:            aws.String(string(probe.HTTPGet.Scheme)),
		//Type: aws.String("CALCULATED"),

	}, nil
}

func (p *route53Provider) getAWSUpdateHealthCheckInput(config *route53.HealthCheckConfig) *route53.UpdateHealthCheckInput {
	return &route53.UpdateHealthCheckInput{
		EnableSNI:                config.EnableSNI,
		FailureThreshold:         config.FailureThreshold,
		FullyQualifiedDomainName: config.FullyQualifiedDomainName,
		Port:                     config.Port,
		//HealthThreshold:          config.HealthThreshold,
		ResourcePath: config.ResourcePath,
		Disabled:     aws.Bool(false),
		Inverted:     aws.Bool(false),
	}
}

func (p *route53Provider) ensureRoute53TrafficPolicy() (string, error) {
	trafficPolicies, err := p.route53Client.ListTrafficPolicies(&route53.ListTrafficPoliciesInput{})
	if err != nil {
		p.log.Error(err, "unable to list route53 traffic policies")
		return "", err
	}
	var currentTrafficPolicy *route53.TrafficPolicy
	for _, trafficPolicySummary := range trafficPolicies.TrafficPolicySummaries {
		if apis.GetKeyShort(p.instance) == *trafficPolicySummary.Name {
			p.log.V(1).Info("found existing", "traffic policy", *trafficPolicySummary.Name)
			currentTrafficPolicyOutput, err := p.route53Client.GetTrafficPolicy(&route53.GetTrafficPolicyInput{
				Id:      aws.String(*trafficPolicySummary.Id),
				Version: aws.Int64(1),
			})
			if err != nil {
				p.log.Error(err, "unable to look up", "policy", apis.GetKeyShort(p.instance))
				return "", err
			}
			currentTrafficPolicy = currentTrafficPolicyOutput.TrafficPolicy
		}
	}
	if currentTrafficPolicy == nil {
		//we need to create a new traffic policy
		p.log.V(1).Info("traffic policy not found creating a new one")
		trafficPolicyID, err := p.createAWSTrafficPolicy()
		if err != nil {
			p.log.Error(err, "unable to create route53 policy")
			return "", err
		}
		return trafficPolicyID, nil
	}
	//we need to check if current taffic policy is current and if not update it

	p.log.V(1).Info("traffic policy found checking if it needs updating")
	newPolicyDocument, err := p.getAWSTrafficPolicyDocument()
	if err != nil {
		p.log.Error(err, "unable to create traffic policy document")
		return "", err
	}
	same, err := p.IsSameTrafficPolicyDocument(newPolicyDocument, *currentTrafficPolicy.Document)
	if err != nil {
		p.log.Error(err, "unable to compare traffic policies")
		return "", err
	}
	if !same {
		// we need to update the health check
		p.log.V(1).Info("traffic needs updating, deleting and recreating")
		err = p.deleteTrafficPolicy(currentTrafficPolicy)
		if err != nil {
			p.log.Error(err, "unable to delete route53 traffic policy")
			return "", err
		}
		trafficPolicyID, err := p.createAWSTrafficPolicy()
		if err != nil {
			p.log.Error(err, "unable to create route53 policy")
			return "", err
		}
		return trafficPolicyID, nil

	}

	p.log.V(1).Info("traffic does not need updating")
	return *currentTrafficPolicy.Id, nil
}

func (p *route53Provider) deleteTrafficPolicy(trafficPolicy *route53.TrafficPolicy) error {
	//first we need to delete all the existing traffic policy instances
	trafficPolicyInstances, err := p.route53Client.ListTrafficPolicyInstancesByPolicy(&route53.ListTrafficPolicyInstancesByPolicyInput{
		TrafficPolicyId:      trafficPolicy.Id,
		TrafficPolicyVersion: aws.Int64(1),
	})
	if err != nil {
		p.log.Error(err, "unable to list traffic policies instances", "by traffic policy id", trafficPolicy.Id)
		return err
	}
	for _, trafficpolicyinstance := range trafficPolicyInstances.TrafficPolicyInstances {
		_, err := p.route53Client.DeleteTrafficPolicyInstance(&route53.DeleteTrafficPolicyInstanceInput{
			Id: trafficpolicyinstance.Id,
		})
		if err != nil {
			p.log.Error(err, "unable to delete route53", " traffic policy instance", trafficpolicyinstance.TrafficPolicyId)
			return err
		}
	}
	//second we need to delete any associated healthchecks
	trafficPolicyDocument := &tpdroute53.Route53TrafficPolicyDocument{}
	err = json.Unmarshal([]byte(*trafficPolicy.Document), trafficPolicyDocument)
	if err != nil {
		p.log.Error(err, "unable to unmarshall traffic policy", "document", *trafficPolicy.Document)
		return err
	}
	endpointRuleRefereces := []tpdroute53.Route53EndpointRuleReference{}
	endpointRuleRefereces = append(endpointRuleRefereces, trafficPolicyDocument.Rules["main"].Items...)
	endpointRuleRefereces = append(endpointRuleRefereces, trafficPolicyDocument.Rules["main"].Locations...)
	endpointRuleRefereces = append(endpointRuleRefereces, trafficPolicyDocument.Rules["main"].Regions...)
	endpointRuleRefereces = append(endpointRuleRefereces, trafficPolicyDocument.Rules["main"].GeoproximityLocations...)
	if trafficPolicyDocument.Rules["main"].Primary != nil {
		endpointRuleRefereces = append(endpointRuleRefereces, *trafficPolicyDocument.Rules["main"].Primary)
	}
	if trafficPolicyDocument.Rules["main"].Secondary != nil {
		endpointRuleRefereces = append(endpointRuleRefereces, *trafficPolicyDocument.Rules["main"].Secondary)
	}

	for _, endpointRuleReference := range endpointRuleRefereces {
		if endpointRuleReference.HealthCheck != "" {
			healthcheckId := strings.Split(endpointRuleReference.HealthCheck, "/")[len(strings.Split(endpointRuleReference.HealthCheck, "/"))-1]
			deleteHealthCheckInput := route53.DeleteHealthCheckInput{
				HealthCheckId: aws.String(healthcheckId),
			}
			p.log.Info("about to delete", "HealthCheck", healthcheckId)
			_, err := p.route53Client.DeleteHealthCheck(&deleteHealthCheckInput)
			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					switch aerr.Code() {
					case route53.ErrCodeNoSuchHealthCheck:
						{
							continue
						}
					default:
						{
							p.log.Error(err, "unable to delete", "healthcheck", healthcheckId)
							return err
						}
					}
				}
			}
		}
	}
	deleteTrafficPolicyInput := route53.DeleteTrafficPolicyInput{
		Id:      trafficPolicy.Id,
		Version: aws.Int64(1),
	}
	_, err = p.route53Client.DeleteTrafficPolicy(&deleteTrafficPolicyInput)
	if err != nil {
		p.log.Error(err, "unable to delete traffic policy", "id", trafficPolicy)
		return err
	}
	return nil
}

func (p *route53Provider) createAWSTrafficPolicy() (string, error) {
	document, err := p.getAWSTrafficPolicyDocument()
	if err != nil {
		p.log.Error(err, "unable to create policy document")
		return "", err
	}
	trafficPolicy := route53.CreateTrafficPolicyInput{
		Document: &document,
		Name:     aws.String(apis.GetKeyShort(p.instance)),
	}
	result, err := p.route53Client.CreateTrafficPolicy(&trafficPolicy)
	if err != nil {
		p.log.Error(err, "unable to create", "network policy", trafficPolicy)
		return "", err
	}
	p.log.V(1).Info("created", "traffic policy id", *result.TrafficPolicy.Id)
	return *result.TrafficPolicy.Id, nil
}

func (p *route53Provider) ensureRoute53DNSRecord(trafficPolicyID string) (string, error) {
	trafficPolicyInstances, err := p.route53Client.ListTrafficPolicyInstancesByHostedZone(&route53.ListTrafficPolicyInstancesByHostedZoneInput{
		HostedZoneId: &p.globalzone.Spec.Provider.Route53.ZoneID,
	})
	if err != nil {
		p.log.Error(err, "unable to list traffic policies instances", "in zone id", p.globalzone.Spec.Provider.Route53.ZoneID)
		return "", err
	}
	var currentTrafficPolicyInstance *route53.TrafficPolicyInstance
	for _, trafficPolicyInstance := range trafficPolicyInstances.TrafficPolicyInstances {
		p.log.V(1).Info("traffic policy instance", "name", *trafficPolicyInstance.Name)
		if *trafficPolicyInstance.Name == p.instance.Spec.Name+"." {
			p.log.V(1).Info("found ", "traffic policy instance", p.instance.Spec.Name)
			currentTrafficPolicyInstanceOutput, err := p.route53Client.GetTrafficPolicyInstance(&route53.GetTrafficPolicyInstanceInput{
				Id: trafficPolicyInstance.Id,
			})
			if err != nil {
				p.log.Error(err, "unable to look up", "traffic policy instance", p.instance.Spec.Name)
				return "", err
			}
			currentTrafficPolicyInstance = currentTrafficPolicyInstanceOutput.TrafficPolicyInstance
		}
	}

	if currentTrafficPolicyInstance == nil {
		//we need to create a new traffic policy
		p.log.V(1).Info("traffic policy instance was not found, creating a new one...", "traffic policy instance", p.instance.Spec.Name)
		policyInstanceID, err := p.createAWSTrafficPolicyInstance(trafficPolicyID)
		if err != nil {
			p.log.Error(err, "unable to create route53 traffic policy instance")
			return "", err
		}
		return policyInstanceID, nil
	}
	//we need to check if current taffic policy instance is current and if not update it
	p.log.V(1).Info("traffic policy instance was found, checking if it is still current", "traffic policy instance", p.instance.Spec.Name)
	if !p.isSameRoute53TrafficPolicyInstance(currentTrafficPolicyInstance) {
		p.log.V(1).Info("found traffic policy not current, deleting it...", "traffic policy instance", p.instance.Spec.Name)
		// we need to update the health check
		_, err := p.route53Client.DeleteTrafficPolicyInstance(&route53.DeleteTrafficPolicyInstanceInput{
			Id: currentTrafficPolicyInstance.Id,
		})
		if err != nil {
			p.log.Error(err, "unable to delete route53", " traffic policy instance", currentTrafficPolicyInstance)
			return "", err
		}
		p.log.V(1).Info("found traffic policy deleted, creating a new one...", "traffic policy instance", p.instance.Spec.Name)
		policyInstanceID, err := p.createAWSTrafficPolicyInstance(trafficPolicyID)
		if err != nil {
			p.log.Error(err, "unable to create route53 traffic policy instance")
			return "", err
		}
		return policyInstanceID, nil
	}
	return *currentTrafficPolicyInstance.Id, nil
}

func (p *route53Provider) isSameRoute53TrafficPolicyInstance(trafficPolicyInstance *route53.TrafficPolicyInstance) bool {
	if *trafficPolicyInstance.HostedZoneId != p.globalzone.Spec.Provider.Route53.ZoneID {
		p.log.V(1).Info("zoneid not the same")
		return false
	}
	if *trafficPolicyInstance.Name != p.instance.Spec.Name+"." {
		p.log.V(1).Info("name not the same")
		return false
	}
	if *trafficPolicyInstance.TTL != int64(p.instance.Spec.TTL) {
		p.log.V(1).Info("ttl not the same")
		return false
	}
	if *trafficPolicyInstance.TrafficPolicyId != p.instance.Status.ProviderStatus.Route53.PolicyID {
		p.log.V(1).Info("policyid not the same")
		return false
	}
	return true
}

func (p *route53Provider) createAWSTrafficPolicyInstance(trafficPolicyID string) (string, error) {
	createTrafficPolicyInstanceInput := &route53.CreateTrafficPolicyInstanceInput{
		HostedZoneId:         &p.globalzone.Spec.Provider.Route53.ZoneID,
		Name:                 &p.instance.Spec.Name,
		TTL:                  aws.Int64(int64(p.instance.Spec.TTL)),
		TrafficPolicyId:      aws.String(trafficPolicyID),
		TrafficPolicyVersion: aws.Int64(1),
	}
	result, err := p.route53Client.CreateTrafficPolicyInstance(createTrafficPolicyInstanceInput)
	if err != nil {
		p.log.Error(err, "unable to create", "traffic policy instance", createTrafficPolicyInstanceInput)
		return "", err
	}
	return *result.TrafficPolicyInstance.Id, nil
}

func (p *route53Provider) IsSameTrafficPolicyDocument(left string, right string) (bool, error) {
	leftTPD := tpdroute53.Route53TrafficPolicyDocument{}
	rightTPD := tpdroute53.Route53TrafficPolicyDocument{}
	err := json.Unmarshal([]byte(left), &leftTPD)
	if err != nil {
		p.log.Error(err, "unable to unmarshal", "traffic policy", left)
		return false, err
	}
	err = json.Unmarshal([]byte(right), &rightTPD)
	if err != nil {
		p.log.Error(err, "unable to unmarshal", "traffic policy", left)
		return false, err
	}
	if leftTPD.AWSPolicyFormatVersion != rightTPD.AWSPolicyFormatVersion {
		return false, nil
	}
	if leftTPD.StartEndpoint != rightTPD.StartEndpoint {
		return false, nil
	}
	if leftTPD.StartRule != rightTPD.StartRule {
		return false, nil
	}
	if leftTPD.RecordType != rightTPD.RecordType {
		return false, nil
	}
	if !reflect.DeepEqual(leftTPD.Endpoints, rightTPD.Endpoints) {
		return false, nil
	}
	if !p.isSameRoute53Rule(leftTPD.Rules["main"], rightTPD.Rules["main"]) {
		return false, nil
	}
	return true, nil
}

func (p *route53Provider) isSameRoute53Rule(left tpdroute53.Route53Rule, right tpdroute53.Route53Rule) bool {
	if left.RuleType != right.RuleType {
		return false
	}
	if !reflect.DeepEqual(left.Primary, right.Primary) {
		return false
	}
	if !reflect.DeepEqual(left.Secondary, right.Secondary) {
		return false
	}

	if !p.isSameRoute53EndpointRuleReferenceSet(left.Locations, right.Locations) {
		return false
	}

	if !p.isSameRoute53EndpointRuleReferenceSet(left.GeoproximityLocations, right.GeoproximityLocations) {
		return false
	}

	if !p.isSameRoute53EndpointRuleReferenceSet(left.Regions, right.Regions) {
		return false
	}

	if !p.isSameRoute53EndpointRuleReferenceSet(left.Items, right.Items) {
		return false
	}
	return true
}

func (p *route53Provider) isSameRoute53EndpointRuleReferenceSet(left []tpdroute53.Route53EndpointRuleReference, right []tpdroute53.Route53EndpointRuleReference) bool {
	return route53endpointrulereferenceset.New(left...).IsEqual(route53endpointrulereferenceset.New(right...))
}

func (p *route53Provider) getAWSTrafficPolicyDocument() (string, error) {
	if !reflect.DeepEqual(p.instance.Spec.HealthCheck, corev1.Probe{}) {
		if p.instance.Status.ProviderStatus.Route53.HealthCheckIDs == nil {
			p.instance.Status.ProviderStatus.Route53.HealthCheckIDs = map[string]string{}
		}
	}
	route53Endpoints := map[string]tpdroute53.Route53Endpoint{}
	switch p.instance.Spec.LoadBalancingPolicy {
	case redhatcopv1alpha1.Multivalue:
		{
			for _, endpoint := range p.endpointMap {
				IPs, err := endpoint.getIPs()
				if err != nil {
					p.log.Error(err, "unable to get IPs for", "endpoint", endpoint)
					return "", err
				}
				for _, IP := range IPs {
					route53Endpoints[endpoint.endpoint.GetKey()+IP] = tpdroute53.Route53Endpoint{
						Type:  tpdroute53.Value,
						Value: IP,
					}
				}
			}
			break
		}
	case redhatcopv1alpha1.Geoproximity:
		{
			for _, endpoint := range p.endpointMap {
				route53Endpoints[endpoint.endpoint.GetKey()] = tpdroute53.Route53Endpoint{
					Type:  tpdroute53.ElasticLoadBalacer,
					Value: endpoint.service.Status.LoadBalancer.Ingress[0].Hostname,
				}
			}
			break
		}
	case redhatcopv1alpha1.Latency:
		{
			for _, endpoint := range p.endpointMap {
				route53Endpoints[endpoint.endpoint.GetKey()] = tpdroute53.Route53Endpoint{
					Type:  tpdroute53.ElasticLoadBalacer,
					Value: endpoint.service.Status.LoadBalancer.Ingress[0].Hostname,
				}
			}
			break
		}
	default:
		{
			err := errors.New("illegal state / unsupported")
			p.log.Error(err, "illegal routing policy", "policy", p.instance.Spec.LoadBalancingPolicy)
			return "", err
		}
	}

	route53Rule := tpdroute53.Route53Rule{}
	switch p.instance.Spec.LoadBalancingPolicy {
	case redhatcopv1alpha1.Multivalue:
		{
			route53Rule.RuleType = tpdroute53.Multivalue
			route53EndpointRuleReferences := []tpdroute53.Route53EndpointRuleReference{}
			for _, endpoint := range p.endpointMap {
				IPs, err := endpoint.getIPs()
				if err != nil {
					p.log.Error(err, "unable to get IPs for", "endpoint", endpoint)
					return "", err
				}
				for _, IP := range IPs {
					route53EndpointRuleReference := tpdroute53.Route53EndpointRuleReference{
						EndpointReference: endpoint.endpoint.GetKey() + IP,
					}
					if p.instance.Spec.HealthCheck != nil {
						healthCheckID, err := p.ensureRoute53HealthCheck(IP)
						if err != nil {
							p.log.Error(err, "unable to create healthcheck for", "endpoint", endpoint)
							return "", err
						}
						route53EndpointRuleReference.HealthCheck = healthCheckID
						route53EndpointRuleReference.EvaluateTargetHealth = true
						p.instance.Status.ProviderStatus.Route53.HealthCheckIDs[p.instance.Spec.Name+"@"+IP] = healthCheckID
					}
					route53EndpointRuleReferences = append(route53EndpointRuleReferences, route53EndpointRuleReference)
				}
			}
			route53Rule.Items = route53EndpointRuleReferences
			break
		}
	case redhatcopv1alpha1.Geoproximity:
		{
			route53Rule.RuleType = tpdroute53.Geoproximity
			route53EndpointRuleReferences := []tpdroute53.Route53EndpointRuleReference{}
			for _, endpoint := range p.endpointMap {
				route53EndpointRuleReference := tpdroute53.Route53EndpointRuleReference{
					EndpointReference: endpoint.endpoint.GetKey(),
					Region:            "aws:route53:" + endpoint.infrastructure.Status.PlatformStatus.AWS.Region,
					Bias:              0,
				}
				if p.instance.Spec.HealthCheck != nil {
					IPs, err := endpoint.getIPs()
					IP := IPs[0]
					if err != nil {
						p.log.Error(err, "unable to get IPs for", "endpoint", endpoint)
						return "", err
					}
					healthCheckID, err := p.ensureRoute53HealthCheck(IP)
					if err != nil {
						p.log.Error(err, "unable to create healthcheck for", "endpoint", endpoint)
						return "", err
					}
					route53EndpointRuleReference.HealthCheck = healthCheckID
					route53EndpointRuleReference.EvaluateTargetHealth = true
					p.instance.Status.ProviderStatus.Route53.HealthCheckIDs[p.instance.Spec.Name+"@"+IP] = healthCheckID
				}
				route53EndpointRuleReferences = append(route53EndpointRuleReferences, route53EndpointRuleReference)

			}
			route53Rule.GeoproximityLocations = route53EndpointRuleReferences
			break
		}
	case redhatcopv1alpha1.Latency:
		{
			route53Rule.RuleType = tpdroute53.Latency
			route53EndpointRuleReferences := []tpdroute53.Route53EndpointRuleReference{}
			for _, endpoint := range p.endpointMap {
				route53EndpointRuleReference := tpdroute53.Route53EndpointRuleReference{
					EndpointReference: endpoint.endpoint.GetKey(),
					Region:            endpoint.infrastructure.Status.PlatformStatus.AWS.Region,
				}
				if p.instance.Spec.HealthCheck != nil {
					IPs, err := endpoint.getIPs()
					IP := IPs[0]
					if err != nil {
						p.log.Error(err, "unable to get IPs for", "endpoint", endpoint)
						return "", err
					}
					healthCheckID, err := p.ensureRoute53HealthCheck(IP)
					if err != nil {
						p.log.Error(err, "unable to create healthcheck for", "endpoint", endpoint)
						return "", err
					}
					route53EndpointRuleReference.HealthCheck = healthCheckID
					route53EndpointRuleReference.EvaluateTargetHealth = true
					p.instance.Status.ProviderStatus.Route53.HealthCheckIDs[p.instance.Spec.Name+"@"+IP] = healthCheckID
				}
				route53EndpointRuleReferences = append(route53EndpointRuleReferences, route53EndpointRuleReference)
			}
			route53Rule.Regions = route53EndpointRuleReferences
			break
		}
	default:
		{
			err := errors.New("illegal state")
			p.log.Error(err, "illegal routing policy")
			return "", err
		}

	}
	route53TrafficPolicyDocument := tpdroute53.Route53TrafficPolicyDocument{
		AWSPolicyFormatVersion: "2015-10-01",
		RecordType:             "A",
		Endpoints:              route53Endpoints,
		StartRule:              "main",
		Rules: map[string]tpdroute53.Route53Rule{
			"main": route53Rule,
		},
	}
	document, err := json.Marshal(route53TrafficPolicyDocument)
	if err != nil {
		p.log.Error(err, "unable to marshal policy document to json", "document", route53TrafficPolicyDocument)
		return "", err
	}
	return string(document), nil
}

func (p *route53Provider) cleanUpRoute53DNSRecord(context context.Context) error {

	trafficPolicies, err := p.route53Client.ListTrafficPolicies(&route53.ListTrafficPoliciesInput{})
	if err != nil {
		p.log.Error(err, "unable to list route53 traffic policies")
		return err
	}
	for _, trafficPolicySummary := range trafficPolicies.TrafficPolicySummaries {
		if apis.GetKeyShort(p.instance) == *trafficPolicySummary.Name {
			p.log.V(1).Info("found existing", "traffic policy", *trafficPolicySummary.Name)
			currentTrafficPolicyOutput, err := p.route53Client.GetTrafficPolicy(&route53.GetTrafficPolicyInput{
				Id:      aws.String(*trafficPolicySummary.Id),
				Version: aws.Int64(1),
			})
			if err != nil {
				p.log.Error(err, "unable to look up", "policy", apis.GetKeyShort(p.instance))
				return err
			}
			err = p.deleteTrafficPolicy(currentTrafficPolicyOutput.TrafficPolicy)
			if err != nil {
				p.log.Error(err, "unable to delete", "traffic policy", apis.GetKeyShort(p.instance))
				return err
			}
		}
	}

	//delete health check
	healthChecks, err := p.route53Client.ListHealthChecks(&route53.ListHealthChecksInput{})
	if err != nil {
		p.log.Error(err, "unable to list route53 healthchecks")
		return err
	}
	if p.instance.Spec.HealthCheck != nil && p.instance.Spec.HealthCheck.HTTPGet != nil {
		for _, healthCheck := range healthChecks.HealthChecks {
			if healthCheck.HealthCheckConfig.FullyQualifiedDomainName != nil && *healthCheck.HealthCheckConfig.FullyQualifiedDomainName == p.instance.Spec.HealthCheck.HTTPGet.Host {
				p.log.V(1).Info("found existing", "health check for", *healthCheck.HealthCheckConfig.FullyQualifiedDomainName)
				_, err := p.route53Client.DeleteHealthCheck(&route53.DeleteHealthCheckInput{
					HealthCheckId: healthCheck.Id,
				})
				if err != nil {
					p.log.Error(err, "unable to delete", "health check for ", *healthCheck.HealthCheckConfig.FullyQualifiedDomainName, "id", healthCheck.Id)
					return err
				}
			}
		}
	}

	return nil
}

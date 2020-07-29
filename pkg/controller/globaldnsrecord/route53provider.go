package globaldnsrecord

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/route53"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/global-load-balancer-operator/pkg/apis/redhatcop/v1alpha1"
	tpdroute53 "github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common/route53"
	"github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common/route53/route53endpointrulereferenceset"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *ReconcileGlobalDNSRecord) createRoute53Record(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (reconcile.Result, error) {

	// validate that all infratsrcutre is homogeneus
	for _, endpointStatus := range endpointMap {
		if endpointStatus.infrastructure.Status.PlatformStatus.Type != ocpconfigv1.AWSPlatformType {
			err := errors.New("Illegal state, only AWS endpoints are allowed")
			log.Error(err, "need aws endpoint", "endpoint type", endpointStatus.infrastructure.Status.PlatformStatus.Type)
			return r.ManageError(instance, endpointMap, err)
		}
	}

	route53Client, err := tpdroute53.GetRoute53Client(globalzone, &r.ReconcilerBase)
	if err != nil {
		log.Error(err, "unable to get route53 client")
		return r.ManageError(instance, endpointMap, err)
	}

	if instance.Status.ProviderStatus.Route53 == nil {
		instance.Status.ProviderStatus.Route53 = &redhatcopv1alpha1.Route53ProviderStatus{}
	}

	// // ensure health check
	// if !reflect.DeepEqual(instance.Spec.HealthCheck, corev1.Probe{}) {
	// 	healthCheckID, err := ensureRoute53HealthCheck(instance, route53Client)
	// 	if err != nil {
	// 		log.Error(err, "unable to ensure the existance of", "healthcheck", instance.Spec.HealthCheck)
	// 		return r.ManageError(instance, endpointMap, err)
	// 	}
	// 	instance.Status.ProviderStatus.Route53.HealthCheckID = healthCheckID
	// }

	// ensure traffic policy
	trafficPolicyID, err := ensureRoute53TrafficPolicy(instance, route53Client, endpointMap)
	if err != nil {
		log.Error(err, "unable to ensure the existance of", "traffic policy", instance.Spec.LoadBalancingPolicy)
		return r.ManageError(instance, endpointMap, err)
	}
	instance.Status.ProviderStatus.Route53.PolicyID = trafficPolicyID
	// ensure dns record
	trafficPolicyInstanceID, err := ensureRoute53DNSRecord(instance, globalzone, route53Client, trafficPolicyID)
	if err != nil {
		log.Error(err, "unable to ensure the existance of", "dns record", instance.Spec.Name)
		return r.ManageError(instance, endpointMap, err)
	}
	instance.Status.ProviderStatus.Route53.PolicyInstanceID = trafficPolicyInstanceID
	return r.ManageSuccess(instance, endpointMap)
}

func ensureRoute53HealthCheck(instance *redhatcopv1alpha1.GlobalDNSRecord, route53Client *route53.Route53, ip string) (string, error) {

	healthChecks, err := route53Client.ListHealthChecks(&route53.ListHealthChecksInput{})
	if err != nil {
		log.Error(err, "unable to list route53 healthchecks")
		return "", err
	}
	var currentHealthCheck *route53.HealthCheck
	for _, healthCheck := range healthChecks.HealthChecks {
		if instance.Spec.HealthCheck.HTTPGet != nil && healthCheck.HealthCheckConfig.FullyQualifiedDomainName != nil && *healthCheck.HealthCheckConfig.FullyQualifiedDomainName == instance.Spec.HealthCheck.HTTPGet.Host && *healthCheck.HealthCheckConfig.IPAddress == ip {
			log.V(1).Info("found existing", "health check for", *healthCheck.HealthCheckConfig.FullyQualifiedDomainName)
			currentHealthCheck = healthCheck
		}
	}

	if currentHealthCheck == nil {
		//we need to create a new healthcheck
		healthCheckID, err := createHealthCheck(instance.Spec.HealthCheck, route53Client, ip)
		if err != nil {
			log.Error(err, "unable to create route53 healthcheck", "for probe", instance.Spec.HealthCheck)
			return "", err
		}
		return healthCheckID, nil
	}

	//if we are here the helth check already exists, we need to check that it's still current
	newHealthCheckConfig, err := getAWSHealthCheckConfig(instance.Spec.HealthCheck, ip)
	if err != nil {
		log.Error(err, "unable to convert probe to aws health check", "probe", instance.Spec.HealthCheck)
		return "", err
	}
	log.V(1).Info("health check was found, checking if it needs update")
	//log.V(1).Info("current", "config", currentHealthCheck.HealthCheckConfig)
	//log.V(1).Info("new", "config", newHealthCheckConfig)
	if !reflect.DeepEqual(newHealthCheckConfig, currentHealthCheck.HealthCheckConfig) {
		// we need to update the health check
		log.V(1).Info("health check needs update, updating")
		updateHealthCheckInput := getAWSUpdateHealthCheckInput(newHealthCheckConfig)
		updateHealthCheckInput.HealthCheckId = currentHealthCheck.Id
		result, err := route53Client.UpdateHealthCheck(updateHealthCheckInput)
		if err != nil {
			log.Error(err, "unable to update aws health check", "probe", updateHealthCheckInput)
			return "", err
		}
		return *result.HealthCheck.Id, nil
	}
	log.V(1).Info("health does not need update")
	return *currentHealthCheck.Id, nil
}

func createHealthCheck(probe *corev1.Probe, route53Client *route53.Route53, ip string) (string, error) {
	healthCheckConfig, err := getAWSHealthCheckConfig(probe, ip)
	if err != nil {
		log.Error(err, "unable to convert probe to aws health check", "probe", probe)
		return "", err
	}
	healthCheckInput := route53.CreateHealthCheckInput{
		CallerReference:   aws.String(*healthCheckConfig.FullyQualifiedDomainName + "@" + *healthCheckConfig.IPAddress),
		HealthCheckConfig: healthCheckConfig,
	}
	result, err := route53Client.CreateHealthCheck(&healthCheckInput)
	if err != nil {
		log.Error(err, "unable to cretae AWS", "health check", healthCheckInput)
		return "", err
	}
	return *result.Location, nil
}

func getAWSHealthCheckConfig(probe *corev1.Probe, ip string) (*route53.HealthCheckConfig, error) {
	if probe == nil || probe.HTTPGet == nil {
		err := errors.New("invalid health check")
		log.Error(err, "healh check must be of http get type")
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

func getAWSUpdateHealthCheckInput(config *route53.HealthCheckConfig) *route53.UpdateHealthCheckInput {
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

func ensureRoute53TrafficPolicy(instance *redhatcopv1alpha1.GlobalDNSRecord, route53Client *route53.Route53, endpointMap map[string]EndpointStatus) (string, error) {
	trafficPolicies, err := route53Client.ListTrafficPolicies(&route53.ListTrafficPoliciesInput{})
	if err != nil {
		log.Error(err, "unable to list route53 traffic policies")
		return "", err
	}
	var currentTrafficPolicy *route53.TrafficPolicy
	for _, trafficPolicySummary := range trafficPolicies.TrafficPolicySummaries {
		if apis.GetKeyShort(instance) == *trafficPolicySummary.Name {
			log.V(1).Info("found existing", "traffic policy", *trafficPolicySummary.Name)
			currentTrafficPolicyOutput, err := route53Client.GetTrafficPolicy(&route53.GetTrafficPolicyInput{
				Id:      aws.String(*trafficPolicySummary.Id),
				Version: aws.Int64(1),
			})
			if err != nil {
				log.Error(err, "unable to look up", "policy", apis.GetKeyShort(instance))
				return "", err
			}
			currentTrafficPolicy = currentTrafficPolicyOutput.TrafficPolicy
		}
	}
	if currentTrafficPolicy == nil {
		//we need to create a new traffic policy
		log.V(1).Info("traffic policy not found creating a new one")
		trafficPolicyID, err := createAWSTrafficPolicy(instance, endpointMap, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 policy")
			return "", err
		}
		return trafficPolicyID, nil
	}
	//we need to check if current taffic policy is current and if not update it

	log.V(1).Info("traffic policy found checking if it needs updating")
	newPolicyDocument, err := getAWSTrafficPolicyDocument(instance, endpointMap, route53Client)
	if err != nil {
		log.Error(err, "unable to create traffic policy document")
		return "", err
	}
	//log.V(1).Info("policy document", "current", *currentTrafficPolicy.Document)
	//log.V(1).Info("policy document", "new", newPolicyDocument)
	// TODO rewrite this one
	same, err := IsSameTrafficPolicyDocument(newPolicyDocument, *currentTrafficPolicy.Document)
	//log.V(1).Info("", "isSame", same)
	//log.V(1).Info("", "reflect", reflect.DeepEqual(newPolicyDocument, *currentTrafficPolicy.Document))
	if err != nil {
		log.Error(err, "unable to compare traffic policies")
		return "", err
	}
	if !same {
		// we need to update the health check
		//TODO first delete any existing traffic policy instances
		log.V(1).Info("traffic needs updating, deleting and recreating")
		err = deleteTrafficPolicy(currentTrafficPolicy, route53Client)
		if err != nil {
			log.Error(err, "unable to delete route53 traffic policy")
			return "", err
		}
		trafficPolicyID, err := createAWSTrafficPolicy(instance, endpointMap, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 policy")
			return "", err
		}
		return trafficPolicyID, nil

	}

	log.V(1).Info("traffic does not need updating")
	return *currentTrafficPolicy.Id, nil
}

func deleteTrafficPolicy(trafficPolicy *route53.TrafficPolicy, route53Client *route53.Route53) error {
	//first we need to delete all the existing traffic policy instances
	trafficPolicyInstances, err := route53Client.ListTrafficPolicyInstancesByPolicy(&route53.ListTrafficPolicyInstancesByPolicyInput{
		TrafficPolicyId:      trafficPolicy.Id,
		TrafficPolicyVersion: aws.Int64(1),
	})
	if err != nil {
		log.Error(err, "unable to list traffic policies instances", "by traffic policy id", trafficPolicy.Id)
		return err
	}
	for _, trafficpolicyinstance := range trafficPolicyInstances.TrafficPolicyInstances {
		_, err := route53Client.DeleteTrafficPolicyInstance(&route53.DeleteTrafficPolicyInstanceInput{
			Id: trafficpolicyinstance.Id,
		})
		if err != nil {
			log.Error(err, "unable to delete route53", " traffic policy instance", trafficpolicyinstance.TrafficPolicyId)
			return err
		}
	}
	//second we need to delete any associated healthchecks
	trafficPolicyDocument := &tpdroute53.Route53TrafficPolicyDocument{}
	err = json.Unmarshal([]byte(*trafficPolicy.Document), trafficPolicyDocument)
	if err != nil {
		log.Error(err, "unable to unmarshall traffic policy", "document", *trafficPolicy.Document)
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
			deleteHealthCheckInput := route53.DeleteHealthCheckInput{
				HealthCheckId: aws.String(endpointRuleReference.HealthCheck),
			}
			_, err := route53Client.DeleteHealthCheck(&deleteHealthCheckInput)
			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					switch aerr.Code() {
					case route53.ErrCodeNoSuchHealthCheck:
						{
							continue
						}
					default:
						{
							log.Error(err, "unable to delete", "healthcheck", endpointRuleReference.HealthCheck)
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
	_, err = route53Client.DeleteTrafficPolicy(&deleteTrafficPolicyInput)
	if err != nil {
		log.Error(err, "unable to delete traffic policy", "id", trafficPolicy)
		return err
	}
	return nil
}

func createAWSTrafficPolicy(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointMap map[string]EndpointStatus, route53Client *route53.Route53) (string, error) {
	document, err := getAWSTrafficPolicyDocument(instance, endpointMap, route53Client)
	if err != nil {
		log.Error(err, "unable to create policy document")
		return "", err
	}
	trafficPolicy := route53.CreateTrafficPolicyInput{
		Document: &document,
		Name:     aws.String(apis.GetKeyShort(instance)),
	}
	result, err := route53Client.CreateTrafficPolicy(&trafficPolicy)
	if err != nil {
		log.Error(err, "unable to create", "network policy", trafficPolicy)
		return "", err
	}
	log.V(1).Info("created", "traffic policy id", *result.TrafficPolicy.Id)
	return *result.TrafficPolicy.Id, nil
}

func ensureRoute53DNSRecord(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, route53Client *route53.Route53, trafficPolicyID string) (string, error) {
	trafficPolicyInstances, err := route53Client.ListTrafficPolicyInstancesByHostedZone(&route53.ListTrafficPolicyInstancesByHostedZoneInput{
		HostedZoneId: &globalzone.Spec.Provider.Route53.ZoneID,
	})
	if err != nil {
		log.Error(err, "unable to list traffic policies instances", "in zone id", globalzone.Spec.Provider.Route53.ZoneID)
		return "", err
	}
	var currentTrafficPolicyInstance *route53.TrafficPolicyInstance
	for _, trafficPolicyInstance := range trafficPolicyInstances.TrafficPolicyInstances {
		log.V(1).Info("traffic policy instance", "name", *trafficPolicyInstance.Name)
		if *trafficPolicyInstance.Name == instance.Spec.Name+"." {
			log.V(1).Info("found ", "traffic policy instance", instance.Spec.Name)
			currentTrafficPolicyInstanceOutput, err := route53Client.GetTrafficPolicyInstance(&route53.GetTrafficPolicyInstanceInput{
				Id: trafficPolicyInstance.Id,
			})
			if err != nil {
				log.Error(err, "unable to look up", "traffic policy instance", instance.Spec.Name)
				return "", err
			}
			currentTrafficPolicyInstance = currentTrafficPolicyInstanceOutput.TrafficPolicyInstance
		}
	}

	if currentTrafficPolicyInstance == nil {
		//we need to create a new traffic policy
		log.V(1).Info("traffic policy instance was not found, creating a new one...", "traffic policy instance", instance.Spec.Name)
		policyInstanceID, err := createAWSTrafficPolicyInstance(instance, globalzone, route53Client, trafficPolicyID)
		if err != nil {
			log.Error(err, "unable to create route53 traffic policy instance")
			return "", err
		}
		return policyInstanceID, nil
	}
	//we need to check if current taffic policy instance is current and if not update it
	log.V(1).Info("traffic policy instance was found, checking if it is still current", "traffic policy instance", instance.Spec.Name)
	if !isSameRoute53TrafficPolicyInstance(currentTrafficPolicyInstance, instance, globalzone) {
		log.V(1).Info("found traffic policy not current, deleting it...", "traffic policy instance", instance.Spec.Name)
		// we need to update the health check
		_, err := route53Client.DeleteTrafficPolicyInstance(&route53.DeleteTrafficPolicyInstanceInput{
			Id: currentTrafficPolicyInstance.Id,
		})
		if err != nil {
			log.Error(err, "unable to delete route53", " traffic policy instance", currentTrafficPolicyInstance)
			return "", err
		}
		log.V(1).Info("found traffic policy deleted, creating a new one...", "traffic policy instance", instance.Spec.Name)
		policyInstanceID, err := createAWSTrafficPolicyInstance(instance, globalzone, route53Client, trafficPolicyID)
		if err != nil {
			log.Error(err, "unable to create route53 traffic policy instance")
			return "", err
		}
		return policyInstanceID, nil
	}
	return *currentTrafficPolicyInstance.Id, nil
}

func isSameRoute53TrafficPolicyInstance(trafficPolicyInstance *route53.TrafficPolicyInstance, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone) bool {
	if *trafficPolicyInstance.HostedZoneId != globalzone.Spec.Provider.Route53.ZoneID {
		log.V(1).Info("zoneid not the same")
		return false
	}
	if *trafficPolicyInstance.Name != instance.Spec.Name+"." {
		log.V(1).Info("name not the same")
		return false
	}
	if *trafficPolicyInstance.TTL != int64(instance.Spec.TTL) {
		log.V(1).Info("ttl not the same")
		return false
	}
	if *trafficPolicyInstance.TrafficPolicyId != instance.Status.ProviderStatus.Route53.PolicyID {
		log.V(1).Info("policyid not the same")
		return false
	}
	return true
}

func getAWSUpdateTrafficPolicyInstanceInput(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone) *route53.UpdateTrafficPolicyInstanceInput {
	return &route53.UpdateTrafficPolicyInstanceInput{
		Id:                   &instance.Status.ProviderStatus.Route53.PolicyInstanceID,
		TTL:                  aws.Int64(int64(instance.Spec.TTL)),
		TrafficPolicyId:      &instance.Status.ProviderStatus.Route53.PolicyID,
		TrafficPolicyVersion: aws.Int64(1),
	}
}

func createAWSTrafficPolicyInstance(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, route53Client *route53.Route53, trafficPolicyID string) (string, error) {
	createTrafficPolicyInstanceInput := &route53.CreateTrafficPolicyInstanceInput{
		HostedZoneId:         &globalzone.Spec.Provider.Route53.ZoneID,
		Name:                 &instance.Spec.Name,
		TTL:                  aws.Int64(int64(instance.Spec.TTL)),
		TrafficPolicyId:      aws.String(trafficPolicyID),
		TrafficPolicyVersion: aws.Int64(1),
	}
	result, err := route53Client.CreateTrafficPolicyInstance(createTrafficPolicyInstanceInput)
	if err != nil {
		log.Error(err, "unable to create", "traffic policy instance", createTrafficPolicyInstanceInput)
		return "", err
	}
	return *result.TrafficPolicyInstance.Id, nil
}

func IsSameTrafficPolicyDocument(left string, right string) (bool, error) {
	leftTPD := tpdroute53.Route53TrafficPolicyDocument{}
	rightTPD := tpdroute53.Route53TrafficPolicyDocument{}
	err := json.Unmarshal([]byte(left), &leftTPD)
	if err != nil {
		log.Error(err, "unable to unmarshal", "traffic policy", left)
		return false, err
	}
	err = json.Unmarshal([]byte(right), &rightTPD)
	if err != nil {
		log.Error(err, "unable to unmarshal", "traffic policy", left)
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
	if !isSameRoute53Rule(leftTPD.Rules["main"], rightTPD.Rules["main"]) {
		return false, nil
	}
	return true, nil
}

func isSameRoute53Rule(left tpdroute53.Route53Rule, right tpdroute53.Route53Rule) bool {
	if left.RuleType != right.RuleType {
		return false
	}
	if !reflect.DeepEqual(left.Primary, right.Primary) {
		return false
	}
	if !reflect.DeepEqual(left.Secondary, right.Secondary) {
		return false
	}

	if !isSameRoute53EndpointRuleReferenceSet(left.Locations, right.Locations) {
		return false
	}

	if !isSameRoute53EndpointRuleReferenceSet(left.GeoproximityLocations, right.GeoproximityLocations) {
		return false
	}

	if !isSameRoute53EndpointRuleReferenceSet(left.Regions, right.Regions) {
		return false
	}

	if !isSameRoute53EndpointRuleReferenceSet(left.Items, right.Items) {
		return false
	}
	return true
}

func isSameRoute53EndpointRuleReferenceSet(left []tpdroute53.Route53EndpointRuleReference, right []tpdroute53.Route53EndpointRuleReference) bool {
	return route53endpointrulereferenceset.New(left...).IsEqual(route53endpointrulereferenceset.New(right...))
}

func getAWSTrafficPolicyDocument(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointMap map[string]EndpointStatus, route53Client *route53.Route53) (string, error) {
	if !reflect.DeepEqual(instance.Spec.HealthCheck, corev1.Probe{}) {
		if instance.Status.ProviderStatus.Route53.HealthCheckIDs == nil {
			instance.Status.ProviderStatus.Route53.HealthCheckIDs = map[string]string{}
		}
	}
	route53Endpoints := map[string]tpdroute53.Route53Endpoint{}
	switch instance.Spec.LoadBalancingPolicy {
	case redhatcopv1alpha1.Multivalue:
		{
			for _, endpoint := range endpointMap {
				IPs, err := endpoint.getIPs()
				if err != nil {
					log.Error(err, "unable to get IPs for", "endpoint", endpoint)
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
			for _, endpoint := range endpointMap {
				route53Endpoints[endpoint.endpoint.GetKey()] = tpdroute53.Route53Endpoint{
					Type:  tpdroute53.ElasticLoadBalacer,
					Value: endpoint.service.Status.LoadBalancer.Ingress[0].Hostname,
				}
			}
			break
		}
	case redhatcopv1alpha1.Latency:
		{
			for _, endpoint := range endpointMap {
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
			log.Error(err, "illegal routing policy", "policy", instance.Spec.LoadBalancingPolicy)
			return "", err
		}
	}

	route53Rule := tpdroute53.Route53Rule{}
	switch instance.Spec.LoadBalancingPolicy {
	case redhatcopv1alpha1.Multivalue:
		{
			route53Rule.RuleType = tpdroute53.Multivalue
			route53EndpointRuleReferences := []tpdroute53.Route53EndpointRuleReference{}
			for _, endpoint := range endpointMap {
				IPs, err := endpoint.getIPs()
				if err != nil {
					log.Error(err, "unable to get IPs for", "endpoint", endpoint)
					return "", err
				}
				for _, IP := range IPs {
					route53EndpointRuleReference := tpdroute53.Route53EndpointRuleReference{
						EndpointReference: endpoint.endpoint.GetKey() + IP,
					}
					if instance.Spec.HealthCheck != nil {
						healthCheckID, err := ensureRoute53HealthCheck(instance, route53Client, IP)
						if err != nil {
							log.Error(err, "unable to create healthcheck for", "endpoint", endpoint)
							return "", err
						}
						route53EndpointRuleReference.HealthCheck = healthCheckID
						instance.Status.ProviderStatus.Route53.HealthCheckIDs[instance.Spec.Name+"@"+IP] = healthCheckID
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
			for _, endpoint := range endpointMap {
				route53EndpointRuleReference := tpdroute53.Route53EndpointRuleReference{
					EndpointReference: endpoint.endpoint.GetKey(),
					Region:            "aws:route53:" + endpoint.infrastructure.Status.PlatformStatus.AWS.Region,
					Bias:              0,
				}
				if instance.Spec.HealthCheck != nil {
					IPs, err := endpoint.getIPs()
					IP := IPs[0]
					if err != nil {
						log.Error(err, "unable to get IPs for", "endpoint", endpoint)
						return "", err
					}
					healthCheckID, err := ensureRoute53HealthCheck(instance, route53Client, IP)
					if err != nil {
						log.Error(err, "unable to create healthcheck for", "endpoint", endpoint)
						return "", err
					}
					route53EndpointRuleReference.HealthCheck = healthCheckID
					instance.Status.ProviderStatus.Route53.HealthCheckIDs[instance.Spec.Name+"@"+IP] = healthCheckID
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
			for _, endpoint := range endpointMap {
				route53EndpointRuleReference := tpdroute53.Route53EndpointRuleReference{
					EndpointReference: endpoint.endpoint.GetKey(),
					Region:            endpoint.infrastructure.Status.PlatformStatus.AWS.Region,
				}
				if instance.Spec.HealthCheck != nil {
					IPs, err := endpoint.getIPs()
					IP := IPs[0]
					if err != nil {
						log.Error(err, "unable to get IPs for", "endpoint", endpoint)
						return "", err
					}
					healthCheckID, err := ensureRoute53HealthCheck(instance, route53Client, IP)
					if err != nil {
						log.Error(err, "unable to create healthcheck for", "endpoint", endpoint)
						return "", err
					}
					route53EndpointRuleReference.HealthCheck = healthCheckID
					instance.Status.ProviderStatus.Route53.HealthCheckIDs[instance.Spec.Name+"@"+IP] = healthCheckID
				}
				route53EndpointRuleReferences = append(route53EndpointRuleReferences, route53EndpointRuleReference)
			}
			route53Rule.Regions = route53EndpointRuleReferences
			break
		}
	default:
		{
			err := errors.New("illegal state")
			log.Error(err, "illegal routing policy")
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
		log.Error(err, "unable to marshal policy document to json", "document", route53TrafficPolicyDocument)
		return "", err
	}
	return string(document), nil
}

func (r *ReconcileGlobalDNSRecord) cleanUpRoute53DNSRecord(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone) error {
	route53Client, err := tpdroute53.GetRoute53Client(globalzone, &r.ReconcilerBase)
	if err != nil {
		log.Error(err, "unable to get route53 client")
		return err
	}

	trafficPolicies, err := route53Client.ListTrafficPolicies(&route53.ListTrafficPoliciesInput{})
	if err != nil {
		log.Error(err, "unable to list route53 traffic policies")
		return err
	}
	for _, trafficPolicySummary := range trafficPolicies.TrafficPolicySummaries {
		if apis.GetKeyShort(instance) == *trafficPolicySummary.Name {
			log.V(1).Info("found existing", "traffic policy", *trafficPolicySummary.Name)
			currentTrafficPolicyOutput, err := route53Client.GetTrafficPolicy(&route53.GetTrafficPolicyInput{
				Id:      aws.String(*trafficPolicySummary.Id),
				Version: aws.Int64(1),
			})
			if err != nil {
				log.Error(err, "unable to look up", "policy", apis.GetKeyShort(instance))
				return err
			}
			err = deleteTrafficPolicy(currentTrafficPolicyOutput.TrafficPolicy, route53Client)
			if err != nil {
				log.Error(err, "unable to delete", "traffic policy", apis.GetKeyShort(instance))
				return err
			}
		}
	}

	//delete health check
	healthChecks, err := route53Client.ListHealthChecks(&route53.ListHealthChecksInput{})
	if err != nil {
		log.Error(err, "unable to list route53 healthchecks")
		return err
	}
	if instance.Spec.HealthCheck != nil && instance.Spec.HealthCheck.HTTPGet != nil {
		for _, healthCheck := range healthChecks.HealthChecks {
			if healthCheck.HealthCheckConfig.FullyQualifiedDomainName != nil && *healthCheck.HealthCheckConfig.FullyQualifiedDomainName == instance.Spec.HealthCheck.HTTPGet.Host {
				log.V(1).Info("found existing", "health check for", *healthCheck.HealthCheckConfig.FullyQualifiedDomainName)
				_, err := route53Client.DeleteHealthCheck(&route53.DeleteHealthCheckInput{
					HealthCheckId: healthCheck.Id,
				})
				if err != nil {
					log.Error(err, "unable to delete", "health check for ", *healthCheck.HealthCheckConfig.FullyQualifiedDomainName, "id", healthCheck.Id)
					return err
				}
			}
		}
	}

	return nil
}

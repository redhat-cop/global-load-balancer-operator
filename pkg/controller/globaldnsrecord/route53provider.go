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
	"github.com/redhat-cop/global-load-balancer-operator/pkg/controller/common"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *ReconcileGlobalDNSRecord) createRoute53Record(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, endpointMap map[string]EndpointStatus) (reconcile.Result, error) {

	//TODO

	// validate that all infratsrcutre is homogeneus
	for _, endpointStatus := range endpointMap {
		if endpointStatus.infrastructure.Status.PlatformStatus.Type != ocpconfigv1.AWSPlatformType {
			err := errors.New("Illegal state, only AWS endpoints are allowed")
			log.Error(err, "need aws endpoint", "endpoint type", endpointStatus.infrastructure.Status.PlatformStatus.Type)
			return r.ManageError(instance, endpointMap, err)
		}
	}

	route53Client, err := common.GetRoute53Client(globalzone, &r.ReconcilerBase)
	if err != nil {
		log.Error(err, "unable to get route53 client")
		return r.ManageError(instance, endpointMap, err)
	}

	// ensure health check
	if !reflect.DeepEqual(instance.Spec.HealthCheck, corev1.Probe{}) {
		err = ensureRoute53HealthCheck(instance, route53Client)
		if err != nil {
			log.Error(err, "unable to ensure the existance of", "healthcheck", instance.Spec.HealthCheck)
			return r.ManageError(instance, endpointMap, err)
		}
	}

	// ensure traffic policy
	err = ensureRoute53TrafficPolicy(instance, route53Client, endpointMap)
	if err != nil {
		log.Error(err, "unable to ensure the existance of", "traffic policy", instance.Spec.LoadBalancingPolicy)
		return r.ManageError(instance, endpointMap, err)
	}

	// ensure dns record
	err = ensureRoute53DNSRecord(instance, globalzone, route53Client)
	if err != nil {
		log.Error(err, "unable to ensure the existance of", "dns record", instance.Spec.Name)
		return r.ManageError(instance, endpointMap, err)
	}

	return r.ManageSuccess(instance, endpointMap)
}

func ensureRoute53HealthCheck(instance *redhatcopv1alpha1.GlobalDNSRecord, route53Client *route53.Route53) error {
	// check if health check exists
	var currentHealthCheckOutput *route53.GetHealthCheckOutput
	if instance.Status.ProviderStatus.Route53 != nil && instance.Status.ProviderStatus.Route53.HealthCheckID != "" {
		var err error
		currentHealthCheckOutput, err = route53Client.GetHealthCheck(&route53.GetHealthCheckInput{
			HealthCheckId: aws.String(instance.Status.ProviderStatus.Route53.HealthCheckID),
		})
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				switch aerr.Code() {
				case route53.ErrCodeNoSuchHealthCheck:
					// we need to create a new health check
					instance.Status.ProviderStatus.Route53.HealthCheckID, err = createHealthCheck(&instance.Spec.HealthCheck, route53Client)
					if err != nil {
						log.Error(err, "unable to create route53 healthcheck", "for probe", instance.Spec.HealthCheck)
						return err
					}
					return nil
				default:
					log.Error(err, "unable to look up", "health check", instance.Status.ProviderStatus.Route53.HealthCheckID)
					return err
				}
			}
		}
	} else {
		// we need to create a new health check
		var err error
		instance.Status.ProviderStatus.Route53.HealthCheckID, err = createHealthCheck(&instance.Spec.HealthCheck, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 healthcheck", "for probe", instance.Spec.HealthCheck)
			return err
		}
		return nil
	}
	//if we are here the helth check already exists, we need to check that it's still current
	newHealthCheck, err := getAWSHealthCheckConfig(&instance.Spec.HealthCheck)
	if err != nil {
		log.Error(err, "unable to convert probe to aws health check", "probe", instance.Spec.HealthCheck)
		return err
	}
	if !reflect.DeepEqual(newHealthCheck, currentHealthCheckOutput.HealthCheck.HealthCheckConfig) {
		// we need to update the health check
		updateHealthCheckInput := getAWSUpdateHealthCheckInput(newHealthCheck)
		err = route53.UpdateHealthCheck(updateHealthCheckInput)
		if err != nil {
			log.Error(err, "unable to update aws health check", "probe", updateHealthCheckInput)
			return err
		}
	}
	return nil
}

func createHealthCheck(probe *corev1.Probe, route53Client *route53.Route53) (string, error) {
	healthCheckConfig, err := getAWSHealthCheckConfig(probe)
	if err != nil {
		log.Error(err, "unable to convert probe to aws health check", "probe", probe)
		return "", err
	}
	healthCheckInput := route53.CreateHealthCheckInput{
		CallerReference:   aws.String(controllerName),
		HealthCheckConfig: healthCheckConfig,
	}
	result, err := route53Client.CreateHealthCheck(&healthCheckInput)
	if err != nil {
		log.Error(err, "unable to cretae AWS", "health check", healthCheckInput)
		return "", err
	}
	return *result.Location, nil
}

func getAWSHealthCheckConfig(probe *corev1.Probe) (*route53.HealthCheckConfig, error) {
	if probe == nil || probe.HTTPGet == nil {
		err := errors.New("invalid health check")
		log.Error(err, "healh check must be of http get type")
		return nil, err
	}
	return &route53.HealthCheckConfig{
		EnableSNI:                aws.Bool(true),
		FailureThreshold:         aws.Int64(int64(probe.FailureThreshold)),
		FullyQualifiedDomainName: &probe.HTTPGet.Host,
		Port:                     aws.Int64(int64(probe.HTTPGet.Port.IntVal)),
		HealthThreshold:          aws.Int64(int64(probe.SuccessThreshold)),
		RequestInterval:          aws.Int64(int64(probe.PeriodSeconds)),
		ResourcePath:             &probe.HTTPGet.Path,
		Type:                     aws.String("HTTPS"),
	}, nil
}

func getAWSUpdateHealthCheckInput(config *route53.HealthCheckConfig) *route53.UpdateHealthCheckInput {
	return &route53.UpdateHealthCheckInput{
		EnableSNI:                config.EnableSNI,
		FailureThreshold:         config.FailureThreshold,
		FullyQualifiedDomainName: config.FullyQualifiedDomainName,
		Port:                     config.Port,
		HealthThreshold:          config.HealthThreshold,
		ResourcePath:             config.ResourcePath,
	}
}

func ensureRoute53TrafficPolicy(instance *redhatcopv1alpha1.GlobalDNSRecord, route53Client *route53.Route53, endpointMap map[string]EndpointStatus) error {
	var currentPolicyOutput *route53.GetTrafficPolicyOutput
	var err error
	if instance.Status.ProviderStatus.Route53 != nil && instance.Status.ProviderStatus.Route53.PolicyID != "" {
		currentPolicyOutput, err = route53Client.GetTrafficPolicy(&route53.GetTrafficPolicyInput{
			Id: aws.String(instance.Status.ProviderStatus.Route53.PolicyID),
		})
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				switch aerr.Code() {
				case route53.ErrCodeNoSuchHealthCheck:
					// we need to create a new traffic policy
					instance.Status.ProviderStatus.Route53.PolicyID, err = createAWSTrafficPolicy(instance, endpointMap, route53Client)
					if err != nil {
						log.Error(err, "unable to create route53 policy")
						return err
					}
					return nil
				default:
					log.Error(err, "unable to look up", "policy", instance.Status.ProviderStatus.Route53.PolicyID)
					return err
				}
			}
		}
	} else {
		// we need to create a new traffic policy
		var err error
		instance.Status.ProviderStatus.Route53.PolicyID, err = createAWSTrafficPolicy(instance, endpointMap, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 policy")
			return err
		}
		return nil
	}
	//if we are here the traffic policy already exists, we need to check that it's still current
	newPolicyDocument, err := getAWSTrafficPolicyDocument(instance, endpointMap)
	if err != nil {
		log.Error(err, "unable to create traffic policy document")
		return err
	}
	if newPolicyDocument != *currentPolicyOutput.TrafficPolicy.Document {
		// we need to update the health check
		err = deleteTrafficPolicy(currentPolicyOutput.TrafficPolicy, route53Client)
		if err != nil {
			log.Error(err, "unable to delete route53 traffic policy")
			return err
		}
		instance.Status.ProviderStatus.Route53.PolicyID, err = createAWSTrafficPolicy(instance, endpointMap, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 policy")
			return err
		}
	}
	return nil
}

func deleteTrafficPolicy(trafficPolicy *route53.TrafficPolicy, route53Client *route53.Route53) error {
	deleteTrafficPolicyInput := route53.DeleteTrafficPolicyInput{
		Id:      trafficPolicy.Id,
		Version: aws.Int64(1),
	}
	_, err := route53Client.DeleteTrafficPolicy(&deleteTrafficPolicyInput)
	if err != nil {
		log.Error(err, "unable to delete traffic policy", "id", trafficPolicy)
		return err
	}
	return nil
}

func createAWSTrafficPolicy(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointMap map[string]EndpointStatus, route53Client *route53.Route53) (string, error) {
	document, err := getAWSTrafficPolicyDocument(instance, endpointMap)
	if err != nil {
		log.Error(err, "unable to create policy document")
		return "", err
	}
	trafficPolicy := route53.CreateTrafficPolicyInput{
		Document: &document,
		Name:     aws.String(apis.GetKeyShort(instance)),
	}
	result, err := route53.CreateTrafficPolicy(trafficPolicy)
	if err != nil {
		log.Error(err, "unable to create", "network policy", trafficPolicy)
		return "", err
	}
	return result.Location, nil
}

func ensureRoute53DNSRecord(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, route53Client *route53.Route53) error {
	var currentPolicyInstanceOutput *route53.GetTrafficPolicyInstanceOutput
	var err error
	if instance.Status.ProviderStatus.Route53 != nil && instance.Status.ProviderStatus.Route53.PolicyInstanceID != "" {
		currentPolicyInstanceOutput, err = route53Client.GetTrafficPolicyInstance(&route53.GetTrafficPolicyInstanceInput{
			Id: aws.String(instance.Status.ProviderStatus.Route53.PolicyInstanceID),
		})
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				switch aerr.Code() {
				case route53.ErrCodeNoSuchHealthCheck:
					// we need to create a new traffic policy instance
					instance.Status.ProviderStatus.Route53.PolicyInstanceID, err = createAWSTrafficPolicyInstance(instance, globalzone, route53Client)
					if err != nil {
						log.Error(err, "unable to create route53 traffic policy instance")
						return err
					}
					return nil
				default:
					log.Error(err, "unable to look up", "traffic policy instance", instance.Status.ProviderStatus.Route53.PolicyInstanceID)
					return err
				}
			}
		}
	} else {
		// we need to create a new traffic policy instance
		var err error
		instance.Status.ProviderStatus.Route53.PolicyInstanceID, err = createAWSTrafficPolicyInstance(instance, globalzone, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 traffic policy instance")
			return err
		}
		return nil
	}
	//if we are here the traffic policy instance exists, we need to check that it's still current
	if !IsSame(currentPolicyInstanceOutput, instance, globalzone) {
		// we need to update the health check
		_, err := route53Client.DeleteTrafficPolicyInstance(&route53.DeleteTrafficPolicyInstanceInput{
			Id: aws.String(instance.Status.ProviderStatus.Route53.PolicyInstanceID),
		})
		if err != nil {
			log.Error(err, "unable to delete route53", " traffic policy instance", currentPolicyInstanceOutput)
			return err
		}
		instance.Status.ProviderStatus.Route53.PolicyInstanceID, err = createAWSTrafficPolicyInstance(instance, globalzone, route53Client)
		if err != nil {
			log.Error(err, "unable to create route53 traffic policy instance")
			return err
		}
	}
	return nil
}

func IsSame(getTrafficPolicyInstanceOutput *route53.GetTrafficPolicyInstanceOutput, instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone) bool {
	if *getTrafficPolicyInstanceOutput.TrafficPolicyInstance.HostedZoneId != globalzone.Spec.Provider.Route53.ZoneID {
		return false
	}
	if *getTrafficPolicyInstanceOutput.TrafficPolicyInstance.Name != instance.Spec.Name {
		return false
	}
	if *getTrafficPolicyInstanceOutput.TrafficPolicyInstance.TTL != int64(instance.Spec.TTL) {
		return false
	}
	if *getTrafficPolicyInstanceOutput.TrafficPolicyInstance.TrafficPolicyId != instance.Status.ProviderStatus.Route53.PolicyID {
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

func createAWSTrafficPolicyInstance(instance *redhatcopv1alpha1.GlobalDNSRecord, globalzone *redhatcopv1alpha1.GlobalDNSZone, route53Client *route53.Route53) (string, error) {
	createTrafficPolicyInstanceInput := &route53.CreateTrafficPolicyInstanceInput{
		HostedZoneId:         &globalzone.Spec.Provider.Route53.ZoneID,
		Name:                 &instance.Spec.Name,
		TTL:                  aws.Int64(int64(instance.Spec.TTL)),
		TrafficPolicyId:      &instance.Status.ProviderStatus.Route53.PolicyID,
		TrafficPolicyVersion: aws.Int64(1),
	}
	result, err := route53Client.CreateTrafficPolicyInstance(createTrafficPolicyInstanceInput)
	if err != nil {
		log.Error(err, "unable to create", "traffic policy instance", createTrafficPolicyInstanceInput)
		return "", err
	}
	return *result.Location, nil
}

func getAWSTrafficPolicyDocument(instance *redhatcopv1alpha1.GlobalDNSRecord, endpointMap map[string]EndpointStatus) (string, error) {
	route53Endpoints := map[string]Route53Endpoint{}
	for _, endpoint := range endpointMap {
		route53Endpoints[getEndpointKey(endpoint.endpoint)] = Route53Endpoint{
			Type:   ElasticLoadBalacer,
			Region: endpoint.infrastructure.Status.PlatformStatus.AWS.Region,
			Value:  endpoint.service.Status.LoadBalancer.Ingress[0].Hostname,
		}
	}

	route53Rule := Route53Rule{}
	switch instance.Spec.LoadBalancingPolicy {
	case redhatcopv1alpha1.Multivalue:
		{
			route53Rule.RuleType = Multivalue
			route53EndpointRuleReferences := []Route53EndpointRuleReference{}
			for _, endpoint := range endpointMap {
				route53EndpointRuleReferences = append(route53EndpointRuleReferences, Route53EndpointRuleReference{
					EndpointReference: getEndpointKey(endpoint.endpoint),
					HealthCheck:       instance.Status.ProviderStatus.Route53.HealthCheckID,
				})
			}
			route53Rule.Items = route53EndpointRuleReferences
			break
		}
	case redhatcopv1alpha1.Proximity:
		{
			route53Rule.RuleType = Geoproximity
			route53EndpointRuleReferences := []Route53EndpointRuleReference{}
			for _, endpoint := range endpointMap {
				route53EndpointRuleReferences = append(route53EndpointRuleReferences, Route53EndpointRuleReference{
					EndpointReference: getEndpointKey(endpoint.endpoint),
					HealthCheck:       instance.Status.ProviderStatus.Route53.HealthCheckID,
					Region:            endpoint.infrastructure.Status.PlatformStatus.AWS.Region,
				})
			}
			route53Rule.Items = route53EndpointRuleReferences
			break
		}
	default:
		{
			err := errors.New("illegal state")
			log.Error(err, "illegal routing policy")
			return "", err
		}

	}
	route53TrafficPolicyDocument := Route53TrafficPolicyDocument{
		AWSPolicyFormatVersion: "2015-10-01",
		RecordType:             "A",
		Endpoints:              route53Endpoints,
		StartRule:              "main",
		Rules: map[string]Route53Rule{
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

type Route53TrafficPolicyDocument struct {
	//"2015-10-01"
	AWSPolicyFormatVersion string `json:"AWSPolicyFormatVersion"`
	// "DNS type for all resource record sets created by this traffic policy",
	RecordType string `json:"RecordType"`
	//"ID that you assign to an endpoint or rule"
	StartEndpoint string                     `json:"StartEndpoint,omitempty"`
	StartRule     string                     `json:"StartRule,omitempty"`
	Endpoints     map[string]Route53Endpoint `json:"Endpoints,omitempty"`
	Rules         map[string]Route53Rule     `json:"Rules,omitempty"`
}

type Route53Endpoint struct {
	//"Type": value | cloudfront | elastic-load-balancer | s3-website
	Type Route53EndpointType `json:"Type"`
	//"Region": "AWS region that you created your Amazon S3 bucket in"
	Region string `json:"Region"`
	//"Value": "value applicable to the type of endpoint"
	Value string `json:"Value"`
}

type Route53EndpointType string

const (
	Value              Route53EndpointType = "value"
	Cloudfront         Route53EndpointType = "cloudfront"
	ElasticLoadBalacer Route53EndpointType = "elastic-load-balancer"
	S3Website          Route53EndpointType = "s3-website"
)

type Route53RuleType string

const (
	Failover     Route53RuleType = "failover"
	Geolocation  Route53RuleType = "geo"
	Geoproximity Route53RuleType = "geoproximity"
	Latency      Route53RuleType = "latency"
	Multivalue   Route53RuleType = "multivalue"
	Weighted     Route53RuleType = "weighted"
)

type Route53Rule struct {
	RuleType              Route53RuleType                `json:"RuleType"`
	Primary               Route53EndpointRuleReference   `json:"Primary,omitempty"`
	Secondary             Route53EndpointRuleReference   `json:"Secondary,omitempty"`
	Locations             []Route53EndpointRuleReference `json:"Locations,omitempty"`
	GeoproximityLocations []Route53EndpointRuleReference `json:"GeoproximityLocations,omitempty"`
	Regions               []Route53EndpointRuleReference `json:"Regions,omitempty"`
	Items                 []Route53EndpointRuleReference `json:"Items,omitempty"`
}

type Route53EndpointRuleReference struct {
	//"EndpointReference | RuleReference": "ID that you assigned to the rule or endpoint that this rule routes traffic to",
	EndpointReference string `json:"EndpointReference,omitempty"`
	RuleReference     string `json:"RuleReference,omitempty"`
	//"EvaluateTargetHealth": "true" | "false",
	EvaluateTargetHealth bool `json:"EvaluateTargetHealth"`
	//"HealthCheck": "optional health check ID"
	HealthCheck string `json:"HealthCheck,omitempty"`

	//geo specific fields
	//"IsDefault": "true" | "false",
	IsDefault bool `json:"IsDefault,omitempty"`
	//"Continent": "continent name,
	Continent string `json:"Continent,omitempty"`
	//"Country": "country name,
	Country string `json:"Country,omitempty"`
	//"Subdivision": "subdivision name,
	Subdivision string `json:"Subdivision,omitempty"`

	//geoproximity & latency specific fields
	//"Region": "AWS Region",
	Region string `json:"Region,omitmepty"`
	//"Latitude": "location south (negative) or north (positive) of the equator, -90 to 90 degrees",
	Latitude int `json:"Latitude,omitmepty"`
	//"Longitude": "location west (negative) or east (positive) of the prime meridian, -180 to 180 degrees",
	Longitude int `json:"Longitude,omitmepty"`
	//"Bias": "optional value to expand or shrink the geographic region for this rule, -99 to 99",
	Bias int `json:"Bias,omitempty"`

	//weighted specific fields
	//"Weight": "value between 0 and 255",
	Weight int `json:"Weight,omitempty"`
}

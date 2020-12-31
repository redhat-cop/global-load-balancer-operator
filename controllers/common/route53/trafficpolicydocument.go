package route53

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
	Region string `json:"Region,omitempty"`
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
	Primary               *Route53EndpointRuleReference  `json:"Primary,omitempty"`
	Secondary             *Route53EndpointRuleReference  `json:"Secondary,omitempty"`
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
	EvaluateTargetHealth bool `json:"EvaluateTargetHealth,omitempty"`
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
	Region string `json:"Region,omitempty"`
	//"Latitude": "location south (negative) or north (positive) of the equator, -90 to 90 degrees",
	Latitude int `json:"Latitude,omitempty"`
	//"Longitude": "location west (negative) or east (positive) of the prime meridian, -180 to 180 degrees",
	Longitude int `json:"Longitude,omitempty"`
	//"Bias": "optional value to expand or shrink the geographic region for this rule, -99 to 99",
	Bias int `json:"Bias,omitempty"`

	//weighted specific fields
	//"Weight": "value between 0 and 255",
	Weight int `json:"Weight,omitempty"`
}

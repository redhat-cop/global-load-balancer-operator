
#!/usr/bin/env bash
#set -x
# Enable xtrace if the DEBUG environment variable is set
if [[ ${DEBUG-} =~ ^1|yes|true$ ]]; then
    set -o xtrace       # Trace the execution of the script (debug)
fi

# A better class of script...
set -o errexit          # Exit on most errors (see the manual)
set -o errtrace         # Make sure any error trap is inherited
set -o nounset          # Disallow expansion of unset variables
set -o pipefail         # Use last non-zero exit code in a pipeline


# source env file 
source "glb_env" || exit 1

# DESC: Usage help
# ARGS: None
# OUTS: None
function script_usage() {
    cat << EOF
Usage:
     -h|--help                  Displays this help
     -v|--verbose               Displays verbose output
    -mv|--multivalue-example             Multivalue example
    -mvhc|--multivalue-with-healthcheck                  Multivalue with healthcheck
    -geohc|--geoproximity-with-healthcheck                  Geoproximity with healthcheck
    -lathc|--latency-with-healthcheck                  Latency with healthcheck
    -rt|--route-autodiscovery                 Route Autodiscovery
    -d|--delete                 delete global dns records from previous examples
EOF
}

function delete_dns_records(){
    echo "delete global dns records from previous examples"
    oc delete globaldnsrecord --all -n ${1}
}

function route_autodiscovery(){
    echo "create global route autodiscovery"
    if [[ ${DEBUG-} =~ ^1|yes|true$ ]]; then
        envsubst < ./docs/scripts/route53-global-route-discovery.yaml > /tmp/result 
        cat  /tmp/result 
    fi
    envsubst < ./docs/scripts/route53-global-route-discovery.yaml | oc apply -f - -n ${1}

    echo "check that global dns records are created"
    sleep 30s
    oc get globaldnsrecord -n ${1}
}

function latency_with_healthcheck(){
    if [[ ${DEBUG-} =~ ^1|yes|true$ ]]; then
        envsubst < ./docs/scripts/route53-latency-global-dns-record-with-healthcheck.yaml > /tmp/result 
        cat  /tmp/result 
    fi
    envsubst < ./docs/scripts/route53-latency-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${1}
    echo "check that global dns records are created"
    sleep 30s
    dig latency-hc.${2}
}

function geoproximity_with_healthcheck(){
    if [[ ${DEBUG-} =~ ^1|yes|true$ ]]; then
        envsubst < ./docs/scripts/route53-geoproximity-global-dns-record-with-healthcheck.yaml > /tmp/result 
        cat  /tmp/result 
    fi
    envsubst < ./docs/scripts/route53-geoproximity-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${1}
    echo "check that global dns records are created"
    sleep 30s
    dig geoproximity-hc.${2}
}

function multivalue_with_healthcheck(){
    if [[ ${DEBUG-} =~ ^1|yes|true$ ]]; then
        envsubst < ./docs/scripts/route53-multivalue-global-dns-record.yaml > /tmp/result 
        cat  /tmp/result 
    fi
    envsubst < ./docs/scripts/route53-multivalue-global-dns-record-with-healthcheck.yaml | oc apply -f - -n ${1}
    echo "check that global dns records are created"
    sleep 30s
    dig multivalue-hc.${global_base_domain}
}

function multivalue_example(){
    if [[ ${DEBUG-} =~ ^1|yes|true$ ]]; then
        envsubst < ./docs/scripts/route53-multivalue-global-dns-record.yaml > /tmp/result 
        cat  /tmp/result 
    fi
    envsubst < ./docs/scripts/route53-multivalue-global-dns-record.yaml | oc apply -f - -n ${1}
    echo "check that global dns records are created"
    sleep 30s
    dig multivalue.${global_base_domain}
}

# DESC: Parameter parser
# ARGS: $@ (optional): Arguments provided to the script
# OUTS: Variables indicating command-line parameters and options
function parse_params() {
    local param
    while [[ $# -gt 0 ]]; do
        param="$1"
        shift
        case $param in
            -h | --help)
                script_usage
                exit 0
                ;;
            -v | --verbose)
                verbose=true
                ;;
            -mv | --multivalue-example)
                 multivalue_example ${namespace} ${global_base_domain}
                ;;
            -mvhc | --multivalue-with-healthcheck)
                multivalue_with_healthcheck ${namespace} ${global_base_domain}
                ;;
            -geohc | --geoproximity-with-healthcheck)
                geoproximity_with_healthcheck ${namespace} ${global_base_domain}
                ;;
            -lathc | --latency-with-healthcheck)
                latency_with_healthcheck ${namespace} ${global_base_domain}
                ;;
            -rt | --route-autodiscovery)
                route_autodiscovery ${namespace}
                ;;
            -d | --delete)
                delete_dns_records ${namespace}
                ;;
            *)
                echo  "Invalid parameter was provided: ${param}" 
                script_usage
                exit 1
                ;;
        esac
    done
}

# DESC: Main control flow
# ARGS: $@ (optional): Arguments provided to the script
# OUTS: None
function main() {
    source glb_env
    if [ -z $@ ];
    then 
      echo "Please pass correct flag."
      script_usage
    fi

    if [ ! -d $HOME/global-load-balancer-operator ];
    then 
        cd $HOME
        git clone https://github.com/redhat-cop/global-load-balancer-operator.git
    fi

    cd $HOME/global-load-balancer-operator
    #export cluster_base_domain=$(oc get dns cluster -o jsonpath='{.spec.baseDomain}')
    #export cluster_zone_id=$(oc get dns cluster -o jsonpath='{.spec.publicZone.id}')
    #export global_base_domain=global.${cluster_base_domain#*.}
    
    export namespace=global-load-balancer-operator
    parse_params "$@"
}

# run main function
main "$@"

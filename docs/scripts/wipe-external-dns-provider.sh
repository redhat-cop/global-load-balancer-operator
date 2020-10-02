#!/bin/bash

oc delete project external-dns
oc delete crd/globaldnsrecords.redhatcop.redhat.io
oc delete crd/globaldnszones.redhatcop.redhat.io
oc delete crd/globalroutediscoveries.redhatcop.redhat.io

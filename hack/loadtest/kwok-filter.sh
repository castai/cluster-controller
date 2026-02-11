#!/bin/bash
# Post-renderer for Helm to filter out FlowSchema and PriorityClass resources
# These resources cause permission errors in some Kubernetes clusters
#
# Usage: helm install ... --post-renderer ./kwok-filter.sh

awk '
BEGIN {
    buffer = ""
    skip = 0
}
/^---$/ {
    if (!skip && buffer != "") {
        print buffer
        print "---"
    }
    buffer = ""
    skip = 0
    next
}
{
    buffer = buffer $0 "\n"
    if ($0 ~ /^kind: (FlowSchema|PriorityClass)$/) {
        skip = 1
    }
}
END {
    if (!skip && buffer != "") {
        print buffer
    }
}
'

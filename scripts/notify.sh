#!/bin/bash

# This command is executed when VRRP instance state transition
TYPE=$1
NAME=$2
STATE=$3

if ! pidof kube-haproxy &>/dev/null;then
    echo "no kube-haproxy process"
    exit 0
fi

# Send SIGUSR1 when Keepalived entering MASTER state
# Send SIGUSR2 when Keepalived entering BACKUP state
case $STATE in
    "MASTER") 
        kill -10 $(pidof kube-haproxy)
        exit 0
        ;;
    "BACKUP") 
        kill -12 $(pidof kube-haproxy)
        exit 0
        ;;
    "FAULT") 
        kill -12 $(pidof kube-haproxy)
        exit 0
        ;;
    *)
        echo "unknown state"
        exit 1
        ;;
esac

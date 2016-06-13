#!/bin/bash

# This command checks haproxy is alive.
if ! pidof haproxy &>/dev/null;then
    service haproxy start &>/dev/null
    sleep 1
    if ! pidof haproxy &>/dev/null;then
        service keepalived stop &>/dev/null
        exit 1
    fi
fi

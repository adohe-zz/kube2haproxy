#!/bin/bash

# This command reloads HAProxy in a efficient manner.
# service haproxy reload is equivalent to: 
# `haproxy -f /etc/haproxy/haproxy.cfg -p /run/haproxy.pid -D -sf $(cat /run/haproxy.pid)`
if pidof haproxy &>/dev/null;then
    service haproxy reload
    echo "haproxy reload done"
fi

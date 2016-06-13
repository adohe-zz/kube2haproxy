#!/bin/bash

if pidof keepalived &>/dev/null;then
    kill -1 `cat /var/run/keepalived.pid`
    echo "keepalived reload done"
fi

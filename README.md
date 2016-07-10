# kube2haproxy

kube2haproxy is a daemon process that automatically configures Keepalived&HAProxy for services deployed on Kubernetes.

It features:
* High Availability HAProxy, support ip failover.
* Auto configure Keepalived&HAProxy configuration files based your template. You can provision your own template in production to enable SSL and notifications.
* Efficient Keepalived&HAProxy reload with controlled rate.
* Developed in Golang, deployment on Keepalived&HAProxy instances has no additional dependency.
* Integrates with [Prometheus](https://github.com/prometheus/prometheus) to monitor metrics.


## Theory of Operation

Compared with kube-proxy(also noted as distributed proxy), kube2haproxy runs locally on node of central HAProxy cluster and is responsible for HAProxy configuration update and reload. kube2haproxy watches `kube-apiserver` for service&endpoints resources change, and reload HAProxy in rate limited manner. To support high availability, we use Keepalived, kube2haproxy is also responsible for Keepalived configuration update and reload. The following diagram demonstrates how this worked in a 2 nodes cluster:

![keepalived_haproxy](./images/arch.png)

## Build

* Step 1: Git clone the kube2haproxy repo: `git clone https://github.com/AdoHe/kube2haproxy.git`
* Step 2: Build with godep: `cd kube2haproxy; godep go build -o kube-haproxy main.go`

## Key command line options

```
--address string                        The IP address to serve on (set to 0.0.0.0 for all interfaces)
--alsologtostderr value                 log to standard error as well as files
--device string                         Network device to bind service IP
--haproxy-config-file string            Path of config file for haproxy (default "/etc/haproxy/haproxy.cfg")
--haproxy-reload-interval duration      Controls how often haproxy reload is invoked (default 5s)
--haproxy-reload-script string          Path of haproxy reload script
--haproxy-template-file string          Path of haproxy template
--keepalived-config-file string         Path of config file for keepalived (default "/etc/keepalived/keepalived.conf")
--keepalived-reload-interval duration   Controls how often keepalived reload is invoked (default 2s)
--keepalived-reload-script string       Path of keepalived reload script
--keepalived-template-file string       Path of keepalived config template
--kube-api-burst int                    Burst to use while talking with kubernets apiserver (default 10)
--kube-api-qps value                    QPS to use while talking with kubernetes apiserver (default 5)
--kubeconfig string                     Path to kubeconfig file with authorization information (the master location is set by the master flag).

```

## Licensing

kube2haproxy is licensed under the Apache License, Version 2.0. See [LICENSE](https://github.com/AdoHe/kube2haproxy/blob/master/LICENSE) for the full
license text.

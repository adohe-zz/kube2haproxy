package app

import (
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/AdoHe/kube2haproxy/app/options"
	"github.com/AdoHe/kube2haproxy/proxy/controller"
	"github.com/AdoHe/kube2haproxy/proxy/template"

	kubeclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	clientcmdapi "k8s.io/kubernetes/pkg/client/unversioned/clientcmd/api"
	"k8s.io/kubernetes/pkg/healthz"
	"k8s.io/kubernetes/pkg/util/wait"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
)

type ProxyServer struct {
	Client     *kubeclient.Client
	Config     *options.ProxyServerConfig
	Controller *controller.ProxyController
}

func NewProxyServer(
	client *kubeclient.Client,
	controller *controller.ProxyController,
	config *options.ProxyServerConfig,
) (*ProxyServer, error) {
	return &ProxyServer{
		Client:     client,
		Config:     config,
		Controller: controller,
	}, nil
}

// NewProxyServerDefault creates a new ProxyServer object with default parameters.
func NewProxyServerDefault(config *options.ProxyServerConfig) (*ProxyServer, error) {
	// Create a Kube Client
	// define api config source
	if config.Kubeconfig == "" && config.Master == "" {
		glog.Warningf("Neither --kubeconfig nor --master was specified. Using default API client. This might not work")
	}
	// This creates a client, first loading any specified kubeconfig
	// file, and then overriding the Master flag, if specified.
	kubeconfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: config.Master}}).ClientConfig()
	if err != nil {
		return nil, err
	}

	// Override kubeconfig qps/burst settings from flags
	kubeconfig.QPS = config.KubeAPIQPS
	kubeconfig.Burst = config.KubeAPIBurst

	client, err := kubeclient.New(kubeconfig)
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}

	proxierConfig := template.ProxierConfig{config.Device, config.KeepalivedConfig, config.HaproxyConfig}
	proxier, err := template.NewProxier(proxierConfig)
	if err != nil {
		return nil, err
	}

	controller := controller.New(client, proxier, config.SyncPeriod)
	return NewProxyServer(client, controller, config)
}

// Run runs the specified ProxyServer. This should never exit.
func (s *ProxyServer) Run() error {
	if s.Config.Port > 0 {
		go func() {
			mux := http.NewServeMux()
			healthz.InstallHandler(mux)
			if s.Config.EnableProfiling {
				mux.HandleFunc("/debug/pprof/", pprof.Index)
				mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
				mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			}
			mux.Handle("/metrics", prometheus.Handler())

			server := &http.Server{
				Addr:    net.JoinHostPort(s.Config.Address, strconv.Itoa(s.Config.Port)),
				Handler: mux,
			}
			glog.Fatal(server.ListenAndServe())
		}()
	}

	s.Controller.Run(wait.NeverStop, registerOSSignals())
	return nil
}

func registerOSSignals() chan os.Signal {
	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGUSR1, syscall.SIGUSR2)
	return c
}

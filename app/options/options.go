package options

import (
	"time"

	utilhaproxy "github.com/adohe/kube2haproxy/util/haproxy"
	utilkeepalived "github.com/adohe/kube2haproxy/util/keepalived"

	"github.com/spf13/pflag"
)

// ProxyServerConfig configures and runs the proxy server.
type ProxyServerConfig struct {
	BindAddress      string
	Address          string
	Port             int
	KubeAPIQPS       float32
	KubeAPIBurst     int
	Master           string
	SyncPeriod       time.Duration
	Kubeconfig       string
	EnableProfiling  bool
	Device           string
	KeepalivedConfig utilkeepalived.KeepalivedConfig
	HaproxyConfig    utilhaproxy.HaproxyConfig
}

func NewProxyServerConfig() *ProxyServerConfig {
	return &ProxyServerConfig{
		KubeAPIQPS:      5.0,
		KubeAPIBurst:    10,
		SyncPeriod:      30 * time.Minute,
		EnableProfiling: false,
		KeepalivedConfig: utilkeepalived.KeepalivedConfig{
			ConfigPath:     "/etc/keepalived/keepalived.conf",
			ReloadInterval: time.Duration(2 * time.Second),
		},
		HaproxyConfig: utilhaproxy.HaproxyConfig{
			ConfigPath:     "/etc/haproxy/haproxy.cfg",
			ReloadInterval: time.Duration(5 * time.Second),
		},
	}
}

// AddFlags adds flags for a specific ProxyServer to the specified FlagSet.
func (s *ProxyServerConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&s.Address, "address", s.Address, "The IP address to serve on (set to 0.0.0.0 for all interfaces)")
	fs.IntVar(&s.Port, "port", s.Port, "The port that the proxy's http service runs on")
	fs.Float32Var(&s.KubeAPIQPS, "kube-api-qps", s.KubeAPIQPS, "QPS to use while talking with kubernetes apiserver")
	fs.IntVar(&s.KubeAPIBurst, "kube-api-burst", s.KubeAPIBurst, "Burst to use while talking with kubernets apiserver")
	fs.StringVar(&s.Master, "master", s.Master, "The address of the kubernetes apiserver")
	fs.DurationVar(&s.SyncPeriod, "sync-period", s.SyncPeriod, "How often configuration from the apiserver is refreshed. Must be greater than 0")
	fs.StringVar(&s.Kubeconfig, "kubeconfig", s.Kubeconfig, "Path to kubeconfig file with authorization information (the master location is set by the master flag).")
	fs.BoolVar(&s.EnableProfiling, "profiling", s.EnableProfiling, "Set true to enable pprof")
	fs.StringVar(&s.Device, "device", s.Device, "Network device to bind service IP")
	fs.StringVar(&s.KeepalivedConfig.ConfigPath, "keepalived-config-file", s.KeepalivedConfig.ConfigPath, "Path of config file for keepalived")
	fs.StringVar(&s.KeepalivedConfig.TemplatePath, "keepalived-template-file", s.KeepalivedConfig.TemplatePath, "Path of keepalived config template")
	fs.DurationVar(&s.KeepalivedConfig.ReloadInterval, "keepalived-reload-interval", s.KeepalivedConfig.ReloadInterval, "Controls how often keepalived reload is invoked")
	fs.StringVar(&s.KeepalivedConfig.ReloadScriptPath, "keepalived-reload-script", s.KeepalivedConfig.ReloadScriptPath, "Path of keepalived reload script")
	fs.StringVar(&s.HaproxyConfig.ConfigPath, "haproxy-config-file", s.HaproxyConfig.ConfigPath, "Path of config file for haproxy")
	fs.StringVar(&s.HaproxyConfig.TemplatePath, "haproxy-template-file", s.HaproxyConfig.TemplatePath, "Path of haproxy template")
	fs.DurationVar(&s.HaproxyConfig.ReloadInterval, "haproxy-reload-interval", s.HaproxyConfig.ReloadInterval, "Controls how often haproxy reload is invoked")
	fs.StringVar(&s.HaproxyConfig.ReloadScriptPath, "haproxy-reload-script", s.HaproxyConfig.ReloadScriptPath, "Path of haproxy reload script")
}

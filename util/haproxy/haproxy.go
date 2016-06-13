package haproxy

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/AdoHe/kube2haproxy/util/config"

	"github.com/golang/glog"
)

// Configuration object for constructing Haproxy.
type HaproxyConfig struct {
	ConfigPath       string
	TemplatePath     string
	ReloadScriptPath string
	ReloadInterval   time.Duration
}

// Haproxy represents a real HAProxy instance.
type Haproxy struct {
	config     HaproxyConfig
	configurer *config.Configurer
}

func NewInstance(cfg HaproxyConfig) (*Haproxy, error) {
	configurer, err := config.NewConfigurer(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &Haproxy{
		config:     cfg,
		configurer: configurer,
	}, nil
}

func (p *Haproxy) Reload(cfgBytes []byte) error {
	// First update haproxy.cfg
	err := p.configurer.WriteConfig(cfgBytes)
	if err != nil {
		return err
	}

	start := time.Now()
	defer func() {
		glog.V(4).Infof("ReloadHaproxy took %v", time.Since(start))
	}()

	// Reload
	glog.V(4).Infof("Ready to reload haproxy")
	cmd := exec.Command(p.config.ReloadScriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error reloading haproxy: %v\n%s", err, out)
	}
	return nil
}

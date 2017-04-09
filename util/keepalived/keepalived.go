package keepalived

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/adohe/kube2haproxy/util/config"

	"github.com/golang/glog"
)

// Configuration object for constructing Keepalived
type KeepalivedConfig struct {
	ConfigPath       string
	TemplatePath     string
	ReloadScriptPath string
	ReloadInterval   time.Duration
}

// Keepalived represents a real keepalived instance.
type Keepalived struct {
	config     KeepalivedConfig
	configurer *config.Configurer
}

func NewInstance(cfg KeepalivedConfig) (*Keepalived, error) {
	configurer, err := config.NewConfigurer(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &Keepalived{
		config:     cfg,
		configurer: configurer,
	}, nil
}

func (k *Keepalived) Reload(cfgBytes []byte) error {
	// First update keepalived.conf
	err := k.configurer.WriteConfig(cfgBytes)
	if err != nil {
		return err
	}

	start := time.Now()
	defer func() {
		glog.V(4).Infof("ReloadKeepalived took %v", time.Since(start))
	}()

	// Reload
	glog.V(4).Infof("Ready to reload keepalived")
	cmd := exec.Command(k.config.ReloadScriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error reloading keepalived: %v\n%s", err, out)
	}

	return nil
}

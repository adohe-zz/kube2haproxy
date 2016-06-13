package config

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/golang/glog"
)

// Configurer knows how to update configuration file located
// at configFilePath.
type Configurer struct {
	configFilePath string
}

func NewConfigurer(configFilePath string) (*Configurer, error) {
	if !canReadFile(configFilePath) {
		return nil, fmt.Errorf("ConfigFile can not be found or not readable")
	}
	return &Configurer{
		configFilePath: configFilePath,
	}, nil
}

func (c *Configurer) WriteConfig(bs []byte) error {
	// first create a backup for current configuration
	if err := c.createConfigBackup(); err != nil {
		glog.Fatalf("failed to create backup")
	}

	tmpConfigFile := fmt.Sprintf("%s.tmp", c.configFilePath)
	err := ioutil.WriteFile(tmpConfigFile, bs, os.FileMode(0644))
	if err != nil {
		glog.Errorf("failed to write tmp config file: %v", err)
		return err
	}

	err = os.Rename(tmpConfigFile, c.configFilePath)
	if err != nil {
		glog.Errorf("failed to rename tmp file: %v", err)
		return err
	}
	return nil
}

func (c *Configurer) createConfigBackup() error {
	cfgContent, err := ioutil.ReadFile(c.configFilePath)
	if err != nil {
		glog.Fatalf("failed to create configuration backup file")
		return err
	}
	backupConfigFileName := fmt.Sprintf("%s.bak", c.configFilePath)
	err = ioutil.WriteFile(backupConfigFileName, cfgContent, os.FileMode(0644))
	if err != nil {
		glog.Fatalf("failed to write backup file: %v", err)
		return err
	}
	return nil
}

// If the file represented by path exists and
// readable, return true otherwise return false.
func canReadFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}

	defer f.Close()

	return true
}

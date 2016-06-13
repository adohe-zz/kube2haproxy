package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/AdoHe/kube2haproxy/app"
	"github.com/AdoHe/kube2haproxy/app/options"

	"k8s.io/kubernetes/pkg/util"

	"github.com/spf13/pflag"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	config := options.NewProxyServerConfig()
	config.AddFlags(pflag.CommandLine)

	util.InitFlags()
	util.InitLogs()
	defer util.FlushLogs()

	s, err := app.NewProxyServerDefault(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if err = s.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
